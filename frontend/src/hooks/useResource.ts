import { useCallback, useEffect, useRef, useState } from 'react'

export function useResource<T>(loader: () => Promise<T>, dependencyKey: string | number = '') {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const loaderRef = useRef(loader)
  useEffect(() => { loaderRef.current = loader }, [loader])
  const refresh = useCallback(async () => {
    try {
      setData(await loaderRef.current())
      setError(null)
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The requested data could not be loaded.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void refresh() }, [dependencyKey, refresh])
  useEffect(() => {
    const update = () => { setLoading(true); void refresh() }
    window.addEventListener('nabu:scope-changed', update)
    return () => window.removeEventListener('nabu:scope-changed', update)
  }, [refresh])
  return { data, setData, loading, error, refresh }
}
