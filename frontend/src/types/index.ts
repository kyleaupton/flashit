// Re-export Wails bindings types for convenience
export type { Drive } from '@flashit/drives/models'
export type { InstallerMeta, StartJobRequest, StartJobResponse } from '@flashit/service/models'
export type { Target, OSFamily, StepInfo } from '@flashit/core/models'

// Import Target for local use in this file
import type { Target } from '@flashit/core/models'

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
  type: 'state' | 'step-start' | 'step-end' | 'progress' | 'log' | 'error'
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

/** Application UI states */
export type AppState =
  | 'empty'       // Initial: no source selected
  | 'source-only' // ISO selected, no drive selected
  | 'ready'       // Both selected, can flash
  | 'in-progress' // Flashing in progress
  | 'complete'    // Successfully finished
  | 'cancelled'   // User cancelled the operation
  | 'error'       // Error occurred

/** Source file info after selection/detection */
export interface SourceInfo {
  path: string
  filename: string
  sizeBytes: number
  installerID: string | null
  installerName: string | null
  detectedTargets: Target[]
}
