import type { ClassValue } from "clsx"
import { clsx } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// Decimal units, as drive makers and file managers on macOS and Linux use.
export function formatSize(bytes: number): string {
  if (bytes >= 1e12) return `${(bytes / 1e12).toFixed(1)} TB`
  if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`
  if (bytes >= 1e6) return `${Math.round(bytes / 1e6)} MB`
  return `${Math.max(1, Math.round(bytes / 1e3))} KB`
}

/** Drive capacities read better rounded: 31.9 GB is sold as 32 GB. */
export function formatCapacity(bytes: number): string {
  const gb = bytes / 1e9
  if (gb >= 10) return `${Math.round(gb)} GB`
  return formatSize(bytes)
}

export function formatDuration(seconds: number): string {
  const s = Math.max(1, Math.round(seconds))
  if (s < 60) return `${s} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min ${s % 60} s`
  return `${Math.floor(m / 60)} h ${m % 60} min`
}

export function basename(path: string): string {
  return path.split(/[\\/]/).pop() ?? path
}

/** "en-US" as "English (US)", falling back to the tag. */
export function languageName(tag: string): string {
  try {
    return new Intl.DisplayNames(['en'], { type: 'language', languageDisplay: 'standard', style: 'short' }).of(tag) ?? tag
  } catch {
    return tag
  }
}

export const isMac = /Mac/.test(navigator.platform)

const nameSuffix = /\s+(USB Device|USB Drive|Flash Drive|Mass Storage Device|Mass Storage|Storage Device|SCSI Disk Device|Media)$/i

/** A drive's model as people call it: no "USB Device" tails, no repeated vendor. */
export function driveName(d: { Model: string; Vendor: string; Device: string }): string {
  const tidy = (s: string) => s.replace(/_/g, ' ').replace(/\s+/g, ' ').trim()
  let model = tidy(d.Model)
  for (let prev = ''; prev !== model; ) {
    prev = model
    model = model.replace(nameSuffix, '').trim()
  }
  const vendor = tidy(d.Vendor)
  if (!model) return vendor || d.Device
  if (!vendor || model.toLowerCase().startsWith(vendor.toLowerCase())) return model
  return `${vendor} ${model}`
}

