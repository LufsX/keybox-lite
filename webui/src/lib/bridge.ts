import { toast as ksuToast, enableEdgeToEdge as ksuEdgeToEdge } from "kernelsu"

export interface ExecResult {
  errno: number
  stdout: string
  stderr: string
  failure?: "unavailable" | "bridge" | "timeout"
}

interface NativeBridge {
  spawn(command: string, args: string, options: string, callback: string): void
}

type BridgeWindow = { ksu?: NativeBridge; [name: string]: unknown }
let callbackCounter = 0

/**
 * Use KernelSU's spawn protocol to stream command output.
 * Native managed shells may serialize jobs; long updates use execUpdate below.
 * kernelsu 3.0.2 emits synchronous startup errors before callers can subscribe;
 * register the native callback ourselves so those errors cannot leave a pending job.
 * A timeout stops waiting only: the native API has no process cancellation method.
 */
export function exec(command: string, onLine?: (line: string) => void, timeoutMs = 15_000, signal?: AbortSignal): Promise<ExecResult> {
  return new Promise((resolve) => {
    let stdout = ""
    let stderr = ""
    let settled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const host = window as unknown as BridgeWindow
    const callback = `keybox_spawn_${Date.now()}_${callbackCounter++}`

    const aborted = () => finish(-1, "bridge", "Command monitor disposed")

    function finish(errno: number, failure?: ExecResult["failure"], detail?: string) {
      if (settled) return
      settled = true
      clearTimeout(timer)
      delete host[callback]
      signal?.removeEventListener("abort", aborted)
      if (detail) stderr += detail + "\n"
      resolve({ errno, stdout, stderr, ...(failure ? { failure } : {}) })
    }

    function data(stream: "stdout" | "stderr", event: string, value: unknown) {
      if (settled || event !== "data" || typeof value !== "string") return
      if (stream === "stdout") stdout += value + "\n"
      else stderr += value + "\n"
      try {
        onLine?.(value)
      } catch (error) {
        // A UI listener must not prevent the process from completing and cleaning up.
        console.error("Command output listener failed", error)
      }
    }

    try {
      if (signal?.aborted) { aborted(); return }
      signal?.addEventListener("abort", aborted)
      if (typeof host.ksu?.spawn !== "function") {
        finish(-1, "unavailable")
        return
      }
      host[callback] = {
        stdout: { emit: (event: string, value: unknown) => data("stdout", event, value) },
        stderr: { emit: (event: string, value: unknown) => data("stderr", event, value) },
        emit(event: string, value: unknown) {
          if (event === "exit") {
            if (typeof value === "number" && Number.isInteger(value) && value >= 0) finish(value)
            else finish(-1, "bridge", "Process exited without a valid exit code")
          } else if (event === "error") {
            finish(-1, "bridge", value instanceof Error ? value.message : String(value))
          }
        },
      }
      timer = setTimeout(() => finish(-1, "timeout"), timeoutMs)
      // KernelSU runs a libsu job inside a managed shell. Keep `exit` (including
      // errors in compound commands) inside a subshell so libsu can still collect
      // the job status. Newlines also protect comments and here-doc terminators.
      host.ksu.spawn("(\n" + command + "\n)\n", "[]", "{}", callback)
    } catch (error) {
      finish(-1, "bridge", error instanceof Error ? error.message : String(error))
    }
  })
}

function quote(value: string): string {
  return "'" + value.replace(/'/g, "'\"'\"'") + "'"
}

/** Run action.sh's supervisor outside the native shell, retaining its exit status. */
export async function execUpdate(
  command: string,
  onOutput: (text: string) => void,
  timeoutMs: number,
  signal: AbortSignal,
  onStarted: (stop: () => Promise<ExecResult>) => void,
  stateDir = "/data/adb/keybox-lite",
): Promise<ExecResult> {
  let dir = ""
  const monitor = new AbortController()
  const visibility = typeof document === "undefined" ? undefined : document
  const sleepers = new Set<() => void>()
  const stopRequests = new Set<Promise<ExecResult>>()
  let finished = false
  let wakePoll: (() => void) | undefined
  let pollRequested = false
  const abort = () => {
    monitor.abort()
    for (const wake of sleepers) wake()
  }
  const visibilityChanged = () => {
    if (visibility?.visibilityState === "hidden") return
    // Keep the request until the next read starts, including during native I/O.
    pollRequested = true
    wakePoll?.()
  }
  const pause = (ms: number, polling = false) => new Promise<void>((resolve) => {
    const wake = () => {
      clearTimeout(timer)
      sleepers.delete(wake)
      if (wakePoll === wake) wakePoll = undefined
      resolve()
    }
    const timer = setTimeout(wake, ms)
    sleepers.add(wake)
    if (polling) wakePoll = wake
    if (finished || monitor.signal.aborted || (polling && pollRequested)) wake()
  })
  signal.addEventListener("abort", abort)
  visibility?.addEventListener("visibilitychange", visibilityChanged)
  const invalid = (detail: string): ExecResult => ({ errno: -1, stdout: "", stderr: detail, failure: "bridge" })
  try {
    if (signal.aborted) return invalid("Update monitor disposed")
    // Redirect all three descriptors before backgrounding. The native shell can
    // finish immediately; the existing supervisor still owns locking/deadlines.
    const worker = 'sh -c ' + quote('exec ' + command) + ' &\npid=$!\n' +
      'if stat=$(cat "/proc/$pid/stat" 2>/dev/null); then\nstat=${stat##*) }\n' +
      'set -- $stat\nshift 19\nprintf "%s %s\\n" "$pid" "$1" > "$dir/process"\nfi\n' +
      'wait "$pid"\nrc=$?\nprintf "%s\\n" "$rc" > "$dir/result.tmp" && mv "$dir/result.tmp" "$dir/result"'
    // Native launch cannot be cancelled. Keep the default 15s wait after
    // disposal so its directory can still be learned and removed in finally.
    const started = await exec(
      'umask 077\ndir=$(mktemp -d /data/local/tmp/keybox-webui.XXXXXXXX) || exit $?\n' +
      ': > "$dir/log" || { rm -rf "$dir"; exit 1; }\n' +
      '(\n' + worker + '\n) </dev/null >"$dir/log" 2>&1 &\nprintf "%s\\n" "$dir"',
    )
    const candidate = started.stdout.trim()
    if (/^\/data\/local\/tmp\/keybox-webui\.[A-Za-z0-9]+$/.test(candidate)) dir = candidate
    if (started.errno !== 0 || started.failure) return started
    if (!dir) return invalid("Invalid update monitor directory")
    if (monitor.signal.aborted) return invalid("Update monitor disposed")
    const path = quote(dir)
    onStarted(async () => {
      // A click during supervisor startup waits for its control record.
      // A completed or rejected launch can never target another update.
      for (let attempt = 0; attempt < 30 && !finished && !monitor.signal.aborted; attempt++) {
        const request = exec(
      'dir=' + path + '\n[ ! -f "$dir/result" ] || exit 1\n' +
      '[ -f "$dir/process" ] || { echo STARTING; exit 0; }\n' +
      'read pid start < "$dir/process" || exit 1\n' +
      'stat=$(cat "/proc/$pid/stat") || exit 1\nstat=${stat##*) }\n' +
      'set -- $stat\nshift 19\n[ "$1" = "$start" ] || exit 1\n' +
      // The published control record establishes readiness. Its child must
      // belong to our supervisor; a competing update cannot be signalled.
      'job=' + quote(stateDir + "/update-job.json") + '\n' +
      '[ -f "$job" ] || { echo STARTING; exit 0; }\n' +
      'child=$(sed -n \'s/.*"pid":\\([0-9]*\\).*/\\1/p\' "$job")\n' +
      'stat=$(cat "/proc/$child/stat") || exit 1\nstat=${stat##*) }\n' +
      'set -- $stat\n[ "$2" = "$pid" ] || { echo STARTING; exit 0; }\nkill -TERM "$pid"',
          undefined, 15_000, monitor.signal,
        )
        stopRequests.add(request)
        const stopped = await request
        stopRequests.delete(request)
        if (stopped.errno !== 0 || stopped.failure || stopped.stdout.trim() !== "STARTING") return stopped
        await pause(100)
      }
      return invalid("Update supervisor is not ready to stop")
    })
    const deadline = Date.now() + timeoutMs
    let previousOutput: string | undefined
    while (!monitor.signal.aborted) {
      pollRequested = false
      const result = await exec(
        'dir=' + path + '\nif [ -f "$dir/result" ]; then cat "$dir/result"; else echo RUNNING; fi\n' +
        'tail -c 80000 "$dir/log"',
        undefined, Math.min(15_000, Math.max(0, deadline - Date.now())), monitor.signal,
      )
      if (monitor.signal.aborted) break
      // A failed polling shell is not the detached operation\'s exit status.
      if (result.errno !== 0 || result.failure) return { ...result, failure: result.failure ?? "bridge" }
      const boundary = result.stdout.indexOf("\n")
      const status = result.stdout.slice(0, boundary)
      const output = result.stdout.slice(boundary + 1)
      if (boundary < 0 || (status !== "RUNNING" && !/^(0|[1-9][0-9]{0,2})$/.test(status)) || Number(status) > 255) {
        return invalid("Invalid update completion status")
      }
      if (output !== previousOutput) {
        previousOutput = output
        onOutput(output)
      }
      if (status !== "RUNNING") {
        finished = true
        for (const wake of sleepers) wake()
        // App awaits stop acknowledgement to classify exit 130 as cancellation.
        // Let an issued stop report its result before disposing native callbacks.
        await Promise.all(stopRequests)
        return { errno: Number(status), stdout: output, stderr: "" }
      }
      const remaining = deadline - Date.now()
      if (remaining <= 0) return { errno: -1, stdout: output, stderr: "", failure: "timeout" }
      await pause(Math.min(visibility?.visibilityState === "hidden" ? 2_500 : 500, remaining), true)
    }
    return invalid("Update monitor disposed")
  } finally {
    finished = true
    signal.removeEventListener("abort", abort)
    visibility?.removeEventListener("visibilitychange", visibilityChanged)
    abort()
    // Unlinking the log also releases disk space if the view is destroyed while
    // the detached supervisor continues. Destruction does not cancel updates.
    if (dir) void exec("rm -rf " + quote(dir))
  }
}

/** Immersive/edge-to-edge: draw the WebUI under the system bars. */
export function enableEdgeToEdge() {
  try {
    ksuEdgeToEdge(true)
  } catch {
    // host without edge-to-edge support
  }
}

export function toast(msg: string, kind: "ok" | "err" = "ok") {
  try {
    ksuToast(msg)
  } catch {
    // not running inside KernelSU
  }
  console.log(kind === "ok" ? "[ok]" : "[err]", msg)
}
