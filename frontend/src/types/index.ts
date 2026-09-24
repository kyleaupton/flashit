// Re-export Wails bindings types for convenience
export type { Drive } from '@flashit/drives/models'
export type { StartJobRequest, StartJobResponse } from '@flashit/service/models'
export type { StepInfo } from '@flashit/core/models'
export type { SourceInfo } from '@flashit/sources/models'
export { Kind as SourceKind } from '@flashit/sources/models'

// Frontend-specific types

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
  type: 'state' | 'step-start' | 'step-end' | 'progress' | 'log' | 'error' | 'authorizing'
  message: string
  step: string
  percent: number
  error: string
  /** Helper error code (proto.ErrorCode) when the failure came from it */
  code?: string
}

/** Step status for UI display */
export type StepStatus = 'pending' | 'running' | 'completed' | 'failed'

/** Step state for tracking in the frontend */
export interface StepState {
  key: string
  name: string
  hasProgress: boolean
  status: StepStatus
  progress: number
  message: string | null
}

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
