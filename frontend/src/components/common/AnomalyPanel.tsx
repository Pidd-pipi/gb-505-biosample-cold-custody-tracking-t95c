import { Alert, Button, Col, Descriptions, Drawer, Form, Input, InputNumber, Modal, Row, Select, Space, Tag, Timeline, Typography, message } from 'antd'
import { useEffect, useState } from 'react'
import { anomalyAPI } from '../../api'
import { StatusBadge } from './StatusBadge'
import { useAuth } from '../../hooks/useAuth'
import type { SpecimenQuarantine, StorageContainer, TemperatureAnomaly } from '../../types/domain'
import { formatDateTime } from '../../utils/format'

export function ReportAnomalyModal({
  open, containers, defaultContainerId, onClose, onReported,
}: {
  open: boolean
  containers: StorageContainer[]
  defaultContainerId?: number
  onClose: () => void
  onReported: () => void
}) {
  const [form] = Form.useForm()
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    if (open) form.setFieldsValue({ storageContainerId: defaultContainerId, temperatureC: undefined, description: '' })
  }, [open, defaultContainerId, form])
  const submit = async () => {
    const values = await form.validateFields()
    setSaving(true)
    try {
      await anomalyAPI.report({
        storageContainerId: values.storageContainerId,
        temperatureC: values.temperatureC,
        inspectedAt: values.inspectedAt?.toISOString(),
        description: values.description || '',
      })
      message.success('超限读数已上报，相关已冻存样本已隔离')
      onClose()
      onReported()
    } finally { setSaving(false) }
  }
  return (
    <Modal title="巡检上报超限读数" width={620} open={open} confirmLoading={saving} onOk={() => void submit()} onCancel={onClose} okText="提交并隔离" cancelText="取消">
      <Alert type="warning" showIcon style={{ marginBottom: 16 }} message="提交后将建立未结冷链异常单，并把该容器内全部已冻存样本转为隔离；同一容器已有未结异常时不能重复建单。" />
      <Form form={form} layout="vertical">
        <Form.Item name="storageContainerId" label="冻存容器" rules={[{ required: true, message: '请选择冻存容器' }]}>
          <Select showSearch optionFilterProp="label" placeholder="选择发生超限的冻存容器" options={containers.map((item) => ({ value: item.id, label: `${item.code} · ${item.name}` }))} />
        </Form.Item>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item name="temperatureC" label="实测温度 (°C)" rules={[{ required: true, message: '请填写实测温度' }]}>
              <InputNumber min={-210} max={40} precision={1} style={{ width: '100%' }} placeholder="如 -55.0" />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="inspectedAt" label="巡检时间（留空取当前）">
              <Input readOnly placeholder="提交时自动记录" />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item name="description" label="异常情况说明"><Input.TextArea rows={3} maxLength={1000} showCount placeholder="记录报警、开门、转运等现场情况" /></Form.Item>
      </Form>
    </Modal>
  )
}

export function AnomalyDrawer({
  anomalyId, onClose, onChanged,
}: {
  anomalyId: number | null
  onClose: () => void
  onChanged: () => void
}) {
  const { user, can } = useAuth()
  const [anomaly, setAnomaly] = useState<TemperatureAnomaly | null>(null)
  const [releaseOpen, setReleaseOpen] = useState(false)
  const [releaseBasis, setReleaseBasis] = useState('')
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    if (anomalyId) void anomalyAPI.get(anomalyId).then(setAnomaly)
    else setAnomaly(null)
  }, [anomalyId])
  const submitRelease = async () => {
    if (!anomaly) return
    if (releaseBasis.trim().length < 3) { message.warning('请填写至少 3 个字符的解除依据'); return }
    setSaving(true)
    try {
      const updated = await anomalyAPI.release(anomaly.id, releaseBasis.trim())
      message.success('异常已解除，涉及样本全部恢复正常')
      setAnomaly(updated)
      setReleaseOpen(false)
      setReleaseBasis('')
      onChanged()
    } finally { setSaving(false) }
  }
  const isReporter = Boolean(anomaly && user && anomaly.reportedById === user.id)
  return (
    <Drawer width={680} title={anomaly ? `冷链异常 ${anomaly.anomalyNo}` : '冷链异常'} open={Boolean(anomalyId)} onClose={onClose}>
      {anomaly && (
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <Descriptions bordered size="small" column={{ xs: 1, md: 2 }}>
            <Descriptions.Item label="状态"><StatusBadge value={anomaly.status} dot /></Descriptions.Item>
            <Descriptions.Item label="涉及样本数">{anomaly.affectedCount}</Descriptions.Item>
            <Descriptions.Item label="冻存容器">{anomaly.storageContainer ? `${anomaly.storageContainer.code} · ${anomaly.storageContainer.name}` : anomaly.storageContainerId}</Descriptions.Item>
            <Descriptions.Item label="温区范围">{anomaly.zoneLowerC} ~ {anomaly.zoneUpperC} °C</Descriptions.Item>
            <Descriptions.Item label="实测温度"><Tag color="error">{anomaly.temperatureC} °C</Tag></Descriptions.Item>
            <Descriptions.Item label="巡检时间">{formatDateTime(anomaly.inspectedAt)}</Descriptions.Item>
            <Descriptions.Item label="上报人">{anomaly.reportedByName}</Descriptions.Item>
            <Descriptions.Item label="上报时间">{formatDateTime(anomaly.createdAt)}</Descriptions.Item>
            {anomaly.status === 'released' && <>
              <Descriptions.Item label="解除人">{anomaly.releasedByName}</Descriptions.Item>
              <Descriptions.Item label="解除时间">{formatDateTime(anomaly.releasedAt)}</Descriptions.Item>
              <Descriptions.Item label="解除依据" span={2}>{anomaly.releaseBasis}</Descriptions.Item>
            </>}
            {anomaly.description && <Descriptions.Item label="异常说明" span={2}>{anomaly.description}</Descriptions.Item>}
          </Descriptions>

          {anomaly.status === 'open' && can('anomaly:manage') && (
            isReporter
              ? <Alert type="info" showIcon message="温度恢复不会自动解除。须由非建单人填写依据后解除，您是建单人，请联系另一位有权限人员处理。" />
              : <Button type="primary" onClick={() => setReleaseOpen(true)}>填写依据并解除</Button>
          )}
          {anomaly.status === 'open' && !can('anomaly:manage') && (
            <Alert type="info" showIcon message="温度恢复不会自动解除，须由保管员复核后手动解除。" />
          )}

          <div>
            <Typography.Title level={5}>样本隔离记录</Typography.Title>
            {anomaly.quarantineEvents?.length ? (
              <Timeline items={groupQuarantine(anomaly.quarantineEvents).map((group) => ({
                color: group.action === 'isolated' ? 'red' : 'green',
                children: (
                  <div className="timeline-item">
                    <div><Typography.Text strong>{group.specimen ? `${group.specimen.accessionNo} · ${group.specimen.sampleType}` : `样本 #${group.specimenId}`}</Typography.Text> <StatusBadge value={group.action} /></div>
                    <Typography.Text>{group.operatorName} · {formatDateTime(group.createdAt)}</Typography.Text>
                    {group.reason && <Typography.Paragraph type="secondary">{group.reason}</Typography.Paragraph>}
                  </div>
                ),
              }))} />
            ) : <Typography.Text type="secondary">暂无隔离样本</Typography.Text>}
          </div>
        </Space>
      )}
      <Modal title="解除冷链异常" open={releaseOpen} confirmLoading={saving} onOk={() => void submitRelease()} onCancel={() => setReleaseOpen(false)} okText="确认解除" cancelText="返回">
        <Alert type="warning" showIcon style={{ marginBottom: 16 }} message="解除将把该异常下全部隔离样本恢复为正常状态；若任一步骤失败，异常单与所有样本保持原状。" />
        <Input.TextArea rows={4} maxLength={1000} showCount value={releaseBasis} onChange={(event) => setReleaseBasis(event.target.value)} placeholder="填写温度恢复依据，如连续 2 小时温度回落至 -80°C、设备维修记录、复核人等" />
      </Modal>
    </Drawer>
  )
}

// 同一样本可能既有隔离又有解除两条记录，逐条展示以保留完整历史。
function groupQuarantine(events: SpecimenQuarantine[]): SpecimenQuarantine[] {
  return [...events].sort((a, b) => (a.createdAt < b.createdAt ? 1 : -1))
}


