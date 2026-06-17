import { memo } from 'react'

export interface HostOption {
  value: string
  label: string
}

interface HostSelectProps {
  options: HostOption[]
  value: string
  onChange: (value: string) => void
  className?: string
}

// Firefox reverts a controlled <select> to its old value when a re-render lands
// between the option's mousedown and the change event (facebook/react#12584).
// This app re-renders constantly (5s polls + /ws/events), so in Firefox the
// host dropdowns appeared stuck on "local". memo with a content-equality check
// keeps the <select> subtree stable across those unrelated re-renders, so no
// render lands in the mousedown→change window.
function sameOptions(a: HostOption[], b: HostOption[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    if (a[i].value !== b[i].value || a[i].label !== b[i].label) return false
  }
  return true
}

export const HostSelect = memo(function HostSelect({ options, value, onChange, className }: HostSelectProps) {
  return (
    <select value={value} onChange={e => onChange(e.target.value)} className={className}>
      {options.map(o => (
        <option key={o.value} value={o.value}>{o.label}</option>
      ))}
    </select>
  )
}, (prev, next) =>
  prev.value === next.value &&
  prev.onChange === next.onChange &&
  prev.className === next.className &&
  sameOptions(prev.options, next.options),
)
