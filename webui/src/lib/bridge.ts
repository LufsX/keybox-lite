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
 * Use KernelSU's spawn protocol so commands stream output without blocking the UI.
 * kernelsu 3.0.2 emits synchronous startup errors before callers can subscribe;
 * register the native callback ourselves so those errors cannot leave a pending job.
 * A timeout stops waiting only: the native API has no process cancellation method.
 */
export function exec(command: string, onLine?: (line: string) => void, timeoutMs = 15_000): Promise<ExecResult> {
  return new Promise((resolve) => {
    let stdout = ""
    let stderr = ""
    let settled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const host = window as unknown as BridgeWindow
    const callback = `keybox_spawn_${Date.now()}_${callbackCounter++}`

    function finish(errno: number, failure?: ExecResult["failure"], detail?: string) {
      if (settled) return
      settled = true
      clearTimeout(timer)
      delete host[callback]
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
