import { onUnmounted, ref, watch, type Ref } from 'vue'

let canvas: HTMLCanvasElement | null = null

/** Rendered width of text in a CSS font shorthand, without touching layout. */
export function textWidth(text: string, font: string): number {
  canvas ??= document.createElement('canvas')
  const ctx = canvas.getContext('2d')
  if (!ctx) return text.length * 7
  ctx.font = font
  return ctx.measureText(text).width
}

export function fontOf(el: Element, size?: string, weight?: string): string {
  const s = getComputedStyle(el)
  return `${weight ?? s.fontWeight} ${size ?? s.fontSize} ${s.fontFamily}`
}

/** An element's content width, kept current as it resizes or is replaced. */
export function useWidth(el: Ref<HTMLElement | null>): Ref<number> {
  const width = ref(0)
  const observer = new ResizeObserver(([entry]) => {
    width.value = entry.contentRect.width
  })
  watch(
    el,
    (now, before) => {
      if (before) observer.unobserve(before)
      if (now) observer.observe(now)
      else width.value = 0
    },
    { immediate: true, flush: 'post' },
  )
  onUnmounted(() => observer.disconnect())
  return width
}

/**
 * The longest version of text, cut in the middle, that fits width once
 * suffix is added after it.
 */
export function fitMiddle(text: string, suffix: string, width: number, font: string): string {
  if (!width || textWidth(text + suffix, font) <= width) return text
  // By code point, so a cut never splits a surrogate pair.
  const chars = Array.from(text)
  let lo = 1
  let hi = chars.length - 1
  let best = '…'
  while (lo <= hi) {
    const keep = Math.floor((lo + hi) / 2)
    const tail = Math.floor(keep / 2)
    const cut = `${chars.slice(0, keep - tail).join('')}…${chars.slice(chars.length - tail).join('')}`
    if (textWidth(cut + suffix, font) <= width) {
      best = cut
      lo = keep + 1
    } else {
      hi = keep - 1
    }
  }
  return best
}

/**
 * The longest start of text that fits width, cut on a code point with any
 * trailing space trimmed before the ellipsis.
 */
export function fitEnd(text: string, width: number, font: string): string {
  if (!width || textWidth(text, font) <= width) return text
  const chars = Array.from(text)
  let lo = 0
  let hi = chars.length - 1
  let best = '…'
  while (lo <= hi) {
    const keep = Math.floor((lo + hi) / 2)
    const cut = `${chars.slice(0, keep).join('').trimEnd()}…`
    if (textWidth(cut, font) <= width) {
      best = cut
      lo = keep + 1
    } else {
      hi = keep - 1
    }
  }
  return best
}
