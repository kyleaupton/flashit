// Re-export Wails bindings types for convenience
export type { Drive } from '@flashit/drives/models'
export type { StartJobRequest, StartJobResponse, Release, UpdaterInfo } from '@flashit/service/models'
export type { StepInfo } from '@flashit/core/models'
export type { SourceInfo } from '@flashit/sources/models'
export { Kind as SourceKind } from '@flashit/sources/models'

/** Mirrors jobs.Status in the Go backend, which is not part of the bindings. */
export enum Status {
  StatusPending = 'pending',
  StatusRunning = 'running',
  StatusSucceeded = 'succeeded',
  StatusFailed = 'failed',
  StatusCancelled = 'cancelled',
}

/** Event payload from backend job:event emissions */
export interface JobEvent {
  jobId: string
  type: 'state' | 'step-start' | 'step-end' | 'progress' | 'log' | 'error' | 'authorizing' | 'warning'
  message: string
  step: string
  percent: number
  error: string
  /** Helper error code (proto.ErrorCode) when the failure came from it */
  code?: string
  bytes?: number
  total?: number
}

export type StepStatus = 'pending' | 'running' | 'completed' | 'failed'

export interface StepState {
  key: string
  name: string
  hasProgress: boolean
  status: StepStatus
  progress: number
  message: string | null
}

/** One drive's progress through the running job. */
export interface DriveProgress {
  step: string | null
  percent: number
  bytes: number | null
  total: number | null
  /** Bytes per second, smoothed; null until there is enough to go on. */
  speed: number | null
  /** Seconds left, smoothed; null until there is enough to go on. */
  eta: number | null
  startedAt: number
}

/**
 * The image slot. 'downloading' is reserved for fetching an image by URL,
 * which is not built yet.
 */
export type SourceStatus = 'empty' | 'probing' | 'ready' | 'unusable' | 'downloading'

/**
 * idle -> source-probed -> target-selected -> running -> done | failed | cancelled.
 * "authorizing" is a substate of running, exposed separately by the job store.
 */
export type AppState =
  | 'idle'
  | 'source-probed'
  | 'target-selected'
  | 'running'
  | 'done'
  | 'failed'
  | 'cancelled'
