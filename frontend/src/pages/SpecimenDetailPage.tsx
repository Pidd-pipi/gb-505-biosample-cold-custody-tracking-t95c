import { ArrowLeftOutlined } from '@ant-design/icons'
import { Alert, Button, Descriptions, Space, Spin, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { specimenAPI } from '../api'
import { CustodyBadge } from '../components/common/CustodyBadge'
import { CustodyTimeline } from '../components/common/CustodyTimeline'
import { EntityTable } from '../components/common/EntityTable'
import { SpecimenIsolationTag } from '../components/common/IsolationBadge'
import { StatusBadge } from '../components/common/StatusBadge'
import type { ProtocolReview, Specimen, SpecimenIsolationEvent } from '../types/domain'
import { formatDateTime } from '../utils/format'

const outcomeLabels: Record<string, string> = { resolved: '解除隔离', invalid: '异常判无效' }

export function SpecimenDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [specimen, setSpecimen] = useState<Specimen | null>(null)
  const [loading, setLoading] = useState(true)
  useEffect(() => { void specimenAPI.get(Number(id)).then(setSpecimen).finally(() => setLoading(false)) }, [id])
  if (loading || !specimen) return <Spin fullscreen />
  const isolationColumns: ColumnsType<SpecimenIsolationEvent> = [
    { title: '异常单号', render: (_: unknown, row: SpecimenIsolationEvent) => row.temperatureAnomaly?.anomalyNo || row.temperatureAnomalyId },
    { title: '隔离时间', dataIndex: 'isolatedAt', render: formatDateTime },
    { title: '隔离操作人', dataIndex: 'isolatedByName' },
    { title: '状态', render: (_: unknown, row: SpecimenIsolationEvent) => row.active
      ? <SpecimenIsolationTag specimen={{ isolated: true }} />
      : <StatusBadge value="anomaly_resolved" /> },
    { title: '解除时间', dataIndex: 'releasedAt', render: formatDateTime },
    { title: '解除操作人', dataIndex: 'releasedByName', render: (value: string) => value || '-' },
    { title: '解除依据', dataIndex: 'releaseBasis', render: (value: string) => value || '-' },
    { title: '结果', dataIndex: 'outcome', render: (value: string) => value ? outcomeLabels[value] || value : '-' },
  ]
  return (
    <div className="page-stack">
      <header className="page-header"><div><Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/specimens')}>返回样本队列</Button><Typography.Title level={2}>{specimen.accessionNo}</Typography.Title></div><Space wrap><CustodyBadge state={specimen.state} />{specimen.isolated && <SpecimenIsolationTag specimen={specimen} />}</Space></header>
      {specimen.isolated && <Alert type="error" showIcon message="样本处于冷链异常隔离中" description={<>关联异常单 {specimen.isolationAnomalyId ? `#${specimen.isolationAnomalyId}` : '-'} · 隔离起始 {formatDateTime(specimen.isolatedAt)}。隔离期间禁止发起交接和批准放行，仍可暂缓或拒绝协议复核；温度恢复后须由非建单人填写依据解除。</>} />}
      <section className="detail-section"><Typography.Title level={4}>样本信息</Typography.Title><Descriptions bordered size="small" column={{ xs: 1, md: 3 }}><Descriptions.Item label="类型">{specimen.sampleType}</Descriptions.Item><Descriptions.Item label="受试者编码">{specimen.subjectCode}</Descriptions.Item><Descriptions.Item label="来源协议">{specimen.protocolCode}</Descriptions.Item><Descriptions.Item label="保管人">{specimen.currentCustodian}</Descriptions.Item><Descriptions.Item label="体积/分装">{specimen.volumeMl} mL / {specimen.aliquotCount} 份</Descriptions.Item><Descriptions.Item label="接收时间">{formatDateTime(specimen.receivedAt)}</Descriptions.Item><Descriptions.Item label="冻存容器">{specimen.storageContainer?.name || '待分配'}</Descriptions.Item><Descriptions.Item label="格位">{specimen.position || '-'}</Descriptions.Item><Descriptions.Item label="温区">{specimen.storageContainer ? <StatusBadge value={specimen.storageContainer.temperatureZone} /> : '-'}</Descriptions.Item></Descriptions></section>
      <section className="detail-section"><Typography.Title level={4}>冷链隔离历史</Typography.Title>{(specimen.isolationEvents?.length || 0) > 0
        ? <EntityTable<SpecimenIsolationEvent> pagination={false} dataSource={specimen.isolationEvents || []} rowKey="id" columns={isolationColumns} />
        : <Typography.Text type="secondary">暂无隔离记录</Typography.Text>}</section>
      <section className="detail-section"><Typography.Title level={4}>交接链</Typography.Title><CustodyTimeline transfers={specimen.transfers || []} /></section>
      <section className="detail-section"><Typography.Title level={4}>协议复核</Typography.Title><EntityTable<ProtocolReview> pagination={false} dataSource={specimen.protocolReviews || []} columns={[{ title: '协议', dataIndex: 'protocolCode' }, { title: '决定', dataIndex: 'decision', render: (value) => <StatusBadge value={value} /> }, { title: '复核人', dataIndex: 'reviewerName' }, { title: '复核时间', dataIndex: 'reviewedAt', render: formatDateTime }, { title: '说明', dataIndex: 'notes' }]} /></section>
    </div>
  )
}
