import { Tooltip } from 'antd'
import { StopOutlined } from '@ant-design/icons'
import type { Specimen } from '../../types/domain'

// QuarantineTag 在样本被冷链异常隔离时显示醒目的隔离标记。
export function QuarantineTag({ specimen, anomalyNo }: { specimen: Specimen; anomalyNo?: string }) {
  if (!specimen.quarantineAnomalyId) return null
  const no = anomalyNo || specimen.quarantineAnomaly?.anomalyNo
  return (
    <Tooltip title={no ? `冷链异常 ${no} 隔离中，禁止交接与放行` : '冷链异常隔离中，禁止交接与放行'}>
      <span className="quarantine-tag"><StopOutlined /> 隔离中{no ? ` · ${no}` : ''}</span>
    </Tooltip>
  )
}
