import { spawn, toast as ksuToast, enableEdgeToEdge as ksuEdgeToEdge } from "kernelsu"

export interface ExecResult {
  errno: number
  stdout: string
  stderr: string
}

/**
 * Run a command through KernelSU's WebUI bridge.
 *
 * We use spawn, not exec: the native exec callback only fires after the whole shell job
 * finishes, which freezes the WebView for the duration (a network update can take
 * seconds). spawn enqueues the job and streams stdout/stderr back as events, so the UI
 * keeps painting. This mirrors what Duck ToolBox does.
 */
export function exec(command: string, onLine?: (line: string) => void): Promise<ExecResult> {
  return new Promise((resolve) => {
    let stdout = ""
    let stderr = ""
    try {
      // Native spawn appends a space after the command even with no args; the newline
      // keeps that space off a possible here-document terminator.
      const child = spawn(command + "\n", [])
      child.stdout.on("data", (data: string) => {
        stdout += data + "\n"
        onLine?.(data)
      })
      child.stderr.on("data", (data: string) => {
        stderr += data + "\n"
        onLine?.(data)
      })
      child.on("exit", (code: number) => resolve({ errno: code, stdout, stderr }))
      child.on("error", (err: unknown) => resolve({ errno: -1, stdout, stderr: String(err) }))
    } catch (e) {
      resolve({ errno: -1, stdout, stderr: String(e) })
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
