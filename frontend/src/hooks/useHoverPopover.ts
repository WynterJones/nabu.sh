import { useCallback, useEffect, useRef } from 'react'

const canHover = () => typeof window.matchMedia !== 'function' || window.matchMedia('(hover: hover) and (pointer: fine)').matches

export function useHoverPopover(setOpen: (open: boolean) => void, closeDelay = 180) {
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const openedFromHover = useRef(false)

  const cancelClose = useCallback(() => {
    if (closeTimer.current) clearTimeout(closeTimer.current)
    closeTimer.current = null
  }, [])

  const openFromHover = useCallback(() => {
    if (!canHover()) return
    cancelClose()
    openedFromHover.current = true
    setOpen(true)
  }, [cancelClose, setOpen])

  const closeFromHover = useCallback(() => {
    if (!canHover()) return
    cancelClose()
    closeTimer.current = setTimeout(() => setOpen(false), closeDelay)
  }, [cancelClose, closeDelay, setOpen])

  const preserveKeyboardFocus = useCallback(() => {
    openedFromHover.current = false
  }, [])

  const preventHoverFocusRestore = useCallback((event: Event) => {
    if (!openedFromHover.current) return
    event.preventDefault()
    openedFromHover.current = false
  }, [])

  useEffect(() => cancelClose, [cancelClose])

  return {
    triggerProps: {
      onPointerEnter: openFromHover,
      onPointerLeave: closeFromHover,
      onKeyDown: preserveKeyboardFocus,
    },
    contentProps: {
      onPointerEnter: openFromHover,
      onPointerLeave: closeFromHover,
      onKeyDown: preserveKeyboardFocus,
      onCloseAutoFocus: preventHoverFocusRestore,
    },
  }
}
