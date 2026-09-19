import { StopOutlined } from '@ant-design/icons'
import { Tag, Tooltip } from 'antd'
import type { Specimen, TemperatureAnomaly } from '../../types/domain'
import { StatusBadge } from './StatusBadge'

export function IsolationTag({ anomaly, count }: { anomaly?: TemperatureAnomaly; count?: number }) {
  if (!anomaly) return null
  const label = count && count > 0 ? `冷链异常 · 隔离 ${count} 份` : '冷链异常隔离中'
  return (
    <Tooltip title={`${anomaly.anomalyNo} · 读数 ${anomaly.recordedC}°C`}>
      <Tag icon={<StopOutlined />} color="error">{label}</Tag>
    </Tooltip>
  )
}

export function SpecimenIsolationTag({ specimen }: { specimen: Pick<Specimen, 'isolated'> }) {
  if (!specimen.isolated) return null
  return (
    <Tooltip title="样本处于冷链异常隔离中：禁止交接与批准放行">
      <Tag icon={<StopOutlined />} color="error">隔离中</Tag>
    </Tooltip>
  )
}

export function AnomalyStateBadge({ state }: { state: TemperatureAnomaly['state'] }) {
  return <StatusBadge value={`anomaly_${state}`} dot />
}
