import { Check, Copy } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button, type ButtonProps } from '@/components/ui/button'

type CopyState = 'idle' | 'copied' | 'failed'

/**
 * Copies `value` to the clipboard and announces the result through a polite
 * live region, so screen reader users hear the confirmation too.
 */
export function CopyButton({
  value,
  label,
  variant = 'outline',
  size = 'sm',
}: {
  value: string
  label: string
  variant?: ButtonProps['variant']
  size?: ButtonProps['size']
}) {
  const [state, setState] = useState<CopyState>('idle')

  useEffect(() => {
    if (state === 'idle') return
    const t = setTimeout(() => setState('idle'), 2000)
    return () => clearTimeout(t)
  }, [state])

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setState('copied')
    } catch {
      setState('failed')
    }
  }

  return (
    <span className="inline-flex items-center gap-2">
      <Button variant={variant} size={size} onClick={() => void copy()}>
        {state === 'copied' ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
        {label}
      </Button>
      <output className="text-xs text-muted-foreground">
        {state === 'copied'
          ? 'Copied'
          : state === 'failed'
            ? 'Copy failed. Select the text and copy it.'
            : ''}
      </output>
    </span>
  )
}
