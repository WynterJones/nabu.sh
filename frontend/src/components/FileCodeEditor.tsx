import CodeMirror from '@uiw/react-codemirror'
import { css } from '@codemirror/lang-css'
import { html } from '@codemirror/lang-html'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { useMemo } from 'react'

export default function FileCodeEditor({ name, value, onChange }: { name: string; value: string; onChange: (value: string) => void }) {
  const extensions = useMemo(() => editorExtensions(name), [name])
  return <CodeMirror value={value} onChange={onChange} extensions={extensions} theme="dark" height="100%" className="file-code-editor" basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: true, highlightSelectionMatches: true }} aria-label={`Editing ${name}`} />
}

function editorExtensions(name: string) {
  const extension = name.split('.').pop()?.toLowerCase()
  if (extension === 'md' || extension === 'mdx') return [markdown()]
  if (extension === 'json' || extension === 'jsonl') return [json()]
  if (extension === 'js' || extension === 'jsx') return [javascript({ jsx: true })]
  if (extension === 'ts' || extension === 'tsx') return [javascript({ jsx: extension === 'tsx', typescript: true })]
  if (extension === 'css') return [css()]
  if (extension === 'html' || extension === 'htm') return [html()]
  return []
}
