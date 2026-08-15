import { forwardRef, useLayoutEffect, useRef, type InputHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { cn } from '../../lib/utils'

export function Field({ label, hint, error, children, className }: { label: string; hint?: string; error?: string; children: React.ReactNode; className?: string }) {
  return (
    <label className={cn('field', className)}>
      <span className="flex items-center justify-between gap-3">
        <span className="field-label">{label}</span>
        {hint ? <span className="text-xs text-muted">{hint}</span> : null}
      </span>
      {children}
      {error ? <span className="text-xs text-danger" role="alert">{error}</span> : null}
    </label>
  )
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...props }, ref) {
  return <input ref={ref} className={cn('input', className)} {...props} />
})

interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  autoSizeMin?: number
  autoSizeMax?: number
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea({ className, value, defaultValue, onInput, style, autoSizeMin = 96, autoSizeMax = 280, ...props }, forwardedRef) {
  const localRef = useRef<HTMLTextAreaElement | null>(null)
  const resize = () => {
    const element = localRef.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.max(autoSizeMin, Math.min(element.scrollHeight, autoSizeMax))}px`
  }
  useLayoutEffect(resize, [value, defaultValue, autoSizeMin, autoSizeMax])
  return (
    <textarea
      ref={(node) => {
        localRef.current = node
        if (typeof forwardedRef === 'function') forwardedRef(node)
        else if (forwardedRef) forwardedRef.current = node
      }}
      className={cn('input min-h-24 resize-none overflow-y-auto py-3', className)}
      style={{ ...style, minHeight: autoSizeMin }}
      value={value}
      defaultValue={defaultValue}
      onInput={(event) => {
        resize()
        onInput?.(event)
      }}
      {...props}
    />
  )
})
