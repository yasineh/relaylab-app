import { Download } from 'lucide-react'

import { buttonVariants } from '@/components/ui/button'
import { formatBytes, hexDump, HEX_PREVIEW_BYTES } from '@/features/events/payload/bytes'

/** Hex dump of the first 64 KiB, with a download of the exact bytes. */
export function HexView({ bytes, downloadHref }: { bytes: Uint8Array; downloadHref: string }) {
  const { text, truncated } = hexDump(bytes)
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
        <span>
          {truncated
            ? `Showing the first ${formatBytes(HEX_PREVIEW_BYTES)} of ${bytes.length.toLocaleString()} bytes.`
            : `${formatBytes(bytes.length)}.`}
        </span>
        <a href={downloadHref} download className={buttonVariants({ variant: 'outline', size: 'sm' })}>
          <Download aria-hidden="true" /> Download raw
        </a>
      </div>
      {bytes.length === 0 ? (
        <p className="text-sm text-muted-foreground">The body is empty.</p>
      ) : (
        <pre
          aria-label="Hex dump"
          className="max-h-[70vh] overflow-auto rounded-md border bg-muted/30 p-3 font-mono text-xs"
        >
          {text}
        </pre>
      )}
    </div>
  )
}
