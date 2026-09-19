import { useCallback, useState } from 'react'
import { anomalyAPI, type PageParams } from '../api'
import type { AnomalyState, PageResult, TemperatureAnomaly } from '../types/domain'

export function useAnomalyStore() {
  const [data, setData] = useState<PageResult<TemperatureAnomaly>>({ items: [], total: 0, page: 1, pageSize: 10 })
  const [loading, setLoading] = useState(false)
  const load = useCallback(async (params: PageParams & { status?: AnomalyState; storageContainerId?: number } = {}) => {
    setLoading(true)
    try { setData(await anomalyAPI.list(params)) } finally { setLoading(false) }
  }, [])
  return { data, loading, load }
}
