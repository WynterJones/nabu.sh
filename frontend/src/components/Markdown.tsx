import { Braces, Check, Clipboard, ShieldCheck } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '../lib/utils'
import { Button } from './ui/Button'
import { useFileViewer } from './FileViewer'

function textContent(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(textContent).join('')
  if (node && typeof node === 'object' && 'props' in node) return textContent((node as { props?: { children?: ReactNode } }).props?.children)
  return ''
}

function CodeBlock({ children }: { children: ReactNode }) {
  const [copied, setCopied] = useState(false)
  const content = textContent(children).replace(/\n$/, '')
  const copy = async () => {
    await navigator.clipboard.writeText(content)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1600)
  }
  return (
    <figure className="markdown-code">
      <Button variant="ghost" size="sm" className="markdown-copy" onClick={() => void copy()} aria-label="Copy code block">
        {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
        {copied ? 'Copied' : 'Copy'}
      </Button>
      <pre tabIndex={0}>{children}</pre>
    </figure>
  )
}

function formatJsonDocument(value: string) {
  const trimmed = value.trim()
  if (!((trimmed.startsWith('{') && trimmed.endsWith('}')) || (trimmed.startsWith('[') && trimmed.endsWith(']')))) return null
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (parsed === null || typeof parsed !== 'object') return null
    return JSON.stringify(parsed, null, 2)
  } catch {
    return null
  }
}

function JsonSyntax({ content }: { content: string }) {
  const tokens: ReactNode[] = []
  const pattern = /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false)\b|\b(null)\b/g
  let cursor = 0
  let token = 0
  for (const match of content.matchAll(pattern)) {
    const start = match.index ?? 0
    if (start > cursor) tokens.push(content.slice(cursor, start))
    const key = `json-token-${token++}`
    if (match[1]) {
      tokens.push(<span className={match[2] ? 'json-token-key' : 'json-token-string'} key={key}>{match[1]}</span>)
      if (match[2]) tokens.push(match[2])
    } else if (match[3]) {
      tokens.push(<span className="json-token-number" key={key}>{match[3]}</span>)
    } else if (match[4]) {
      tokens.push(<span className="json-token-boolean" key={key}>{match[4]}</span>)
    } else {
      tokens.push(<span className="json-token-null" key={key}>{match[5]}</span>)
    }
    cursor = start + match[0].length
  }
  if (cursor < content.length) tokens.push(content.slice(cursor))
  return <>{tokens}</>
}

function JsonBlock({ content }: { content: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    await navigator.clipboard.writeText(content)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1600)
  }
  return (
    <figure className="markdown-json" aria-label="Formatted JSON">
      <figcaption className="markdown-json-toolbar">
        <span className="markdown-json-label"><Braces className="size-3.5" />JSON</span>
        <Button variant="ghost" size="sm" className="h-7 px-2 text-[11px]" onClick={() => void copy()} aria-label="Copy JSON">
          {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </figcaption>
      <pre tabIndex={0}><code><JsonSyntax content={content} /></code></pre>
    </figure>
  )
}

function isWorkspaceFileLink(href: string) {
  return Boolean(href) && !/^(?:https?:|mailto:|tel:|#|\/api\/)/i.test(href)
}

function looksLikeWorkspaceFile(value: string) {
  const text = value.trim()
  return !text.includes('\n') && (/^(?:\.?\.?\/|\/)[^\s]+/.test(text) || /^[^\s/]+(?:\/[^\s]+)+$/.test(text) || /^[^\s/]+\.[A-Za-z0-9]{1,8}$/.test(text))
}

const redactionHref = '#nabu-redacted'

function decorateRedactions(markdown: string) {
  return markdown
    .split(/(```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`)/g)
    .map((section, index) => index % 2 === 0
      ? section.replace(/\[REDACTED\]/gi, `[Redacted](${redactionHref})`)
      : section)
    .join('')
}

function RedactedToken() {
  return (
    <span className="redacted-token" role="note" aria-label="Sensitive value redacted">
      <ShieldCheck className="redacted-token-icon" aria-hidden="true" />
      <span className="redacted-token-mask" aria-hidden="true">••••••</span>
      <span className="redacted-token-label" aria-hidden="true">Redacted</span>
    </span>
  )
}

export function Markdown({ children, className }: { children: string; className?: string }) {
  const { openFile } = useFileViewer()
  const json = formatJsonDocument(children)
  if (json) return <div className={cn('markdown', className)}><JsonBlock content={json} /></div>
  const components: Components = {
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  code: ({ className, children, ...props }) => {
    const content = textContent(children)
    if (!className && looksLikeWorkspaceFile(content)) return <button type="button" className="file-link" onClick={() => openFile(content)}><code className="markdown-inline-code" {...props}>{children}</code></button>
    return <code className={cn(className, !className && 'markdown-inline-code')} {...props}>{children}</code>
  },
  a: ({ href = '', children, ...props }) => {
    if (href === redactionHref) return <RedactedToken />
    const external = /^https?:\/\//i.test(href)
    if (isWorkspaceFileLink(href)) return <button type="button" className="file-link" onClick={() => openFile(href)}>{children}</button>
    return <a href={href} {...(external ? { target: '_blank', rel: 'noreferrer' } : {})} {...props}>{children}</a>
  },
  img: ({ src = '', alt = '', ...props }) => <img src={src} alt={alt} loading="lazy" {...props} />,
  table: ({ children, ...props }) => <div className="markdown-table-scroll" tabIndex={0}><table {...props}>{children}</table></div>,
  th: ({ children, ...props }) => {
    const label = textContent(children)
    return <th {...props}><span className="markdown-table-cell" title={label}>{children}</span></th>
  },
  td: ({ children, ...props }) => {
    const label = textContent(children)
    return <td {...props}><span className="markdown-table-cell" title={label}>{children}</span></td>
  },
  }
  return (
    <div className={cn('markdown', className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>{decorateRedactions(children)}</ReactMarkdown>
    </div>
  )
}
