import { ExclamationCircleOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { Button, Input, Segmented, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { useEffect, useState } from 'react'
import { usePagination } from '../hooks/usePagination'
import { useAnomalyStore } from '../stores/anomalyStore'
import { AnomalyDrawer, ReportAnomalyModal } from '../components/common/AnomalyPanel'
import { EntityTable } from '../components/common/EntityTable'
import { StatusBadge } from '../components/common/StatusBadge'
import { useAuth } from '../hooks/useAuth'
import { storageAPI } from '../api'
import type { AnomalyState, StorageContainer, TemperatureAnomaly } from '../types/domain'
import { formatDateTime } from '../utils/format'

export function AnomaliesPage() {
  const { data, loading, load } = useAnomalyStore()
  const pagination = usePagination()
  const { can } = useAuth()
  const [search, setSearch] = useState('')
  const [status, setStatus] = useState<AnomalyState>('open')
  const [containers, setContainers] = useState<StorageContainer[]>([])
  const [reportOpen, setReportOpen] = useState(false)
  const [anomalyId, setAnomalyId] = useState<number | null>(null)
  const refresh = () => load({ page: pagination.page, pageSize: pagination.pageSize, search, status })
  useEffect(() => { void refresh() }, [pagination.page, pagination.pageSize, status])
  useEffect(() => { void storageAPI.list({ page: 1, pageSize: 100 }).then((result) => setContainers(result.items)) }, [])
  const columns: ColumnsType<TemperatureAnomaly> = [
    {
      title: '异常单号', dataIndex: 'anomalyNo', fixed: 'left', render: (value, row) => (
        <Button type="link" className="table-link" onClick={() => setAnomalyId(row.id)}>
          {row.status === 'open' && <ExclamationCircleOutlined style={{ color: '#cf1322' }} />} {value}
        </Button>
      ),
    },
    { title: '冻存容器', render: (_, row) => <div><strong>{row.storageContainer?.code || row.storageContainerId}</strong><small className="cell-subtitle">{row.storageContainer?.name}</small></div> },
    { title: '实测温度', dataIndex: 'temperatureC', render: (value) => <Typography.Text strong type="danger">{value} °C</Typography.Text> },
    { title: '温区范围', render: (_, row) => `${row.zoneLowerC} ~ ${row.zoneUpperC} °C` },
    { title: '涉及样本', dataIndex: 'affectedCount', render: (value) => `${value} 份` },
    { title: '状态', dataIndex: 'status', render: (value) => <StatusBadge value={value} dot /> },
    { title: '上报人', dataIndex: 'reportedByName' },
    { title: '巡检时间', dataIndex: 'inspectedAt', render: formatDateTime },
    { title: '解除人/时间', render: (_, row) => row.status === 'released' ? <div>{row.releasedByName}<small className="cell-subtitle">{formatDateTime(row.releasedAt)}</small></div> : '-' },
    { title: '操作', fixed: 'right', render: (_, row) => <Button size="small" onClick={() => setAnomalyId(row.id)}>查看闭环</Button> },
  ]
  return (
    <div className="page-stack">
      <header className="page-header">
        <div><Typography.Title level={2}>冷链异常隔离</Typography.Title><Typography.Text type="secondary">超限读数自动隔离容器内已冻存样本；温度恢复不自动解除，须由非建单人填写依据闭环</Typography.Text></div>
        {can('anomaly:manage') && <Button type="primary" danger icon={<ExclamationCircleOutlined />} onClick={() => setReportOpen(true)}>巡检超限上报</Button>}
      </header>
      <div className="table-toolbar">
        <Input allowClear prefix={<SearchOutlined />} placeholder="搜索异常单号、说明或人员" value={search} onChange={(event) => setSearch(event.target.value)} onPressEnter={() => void refresh()} />
        <Segmented value={status} onChange={(value) => setStatus(value as AnomalyState)} options={[{ value: 'open', label: '未结' }, { value: 'released', label: '已解除' }]} />
        <Button icon={<ReloadOutlined />} onClick={() => void refresh()}>刷新</Button>
      </div>
      <EntityTable columns={columns} dataSource={data.items} loading={loading} emptyTitle={status === 'open' ? '当前没有未结冷链异常' : '暂无已解除异常'} pagination={{ current: pagination.page, pageSize: pagination.pageSize, total: data.total, onChange: pagination.update, showSizeChanger: true }} />
      <ReportAnomalyModal open={reportOpen} containers={containers} onClose={() => setReportOpen(false)} onReported={() => { setStatus('open'); void refresh() }} />
      <AnomalyDrawer anomalyId={anomalyId} onClose={() => setAnomalyId(null)} onChanged={() => void refresh()} />
    </div>
  )
}
