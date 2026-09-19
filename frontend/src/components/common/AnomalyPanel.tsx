import { ExclamationCircleOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { Alert, Button, Col, DatePicker, Drawer, Form, Input, InputNumber, Radio, Row, Space, Timeline, Typography, message } from 'antd'
import dayjs from 'dayjs'
import { useEffect, useState } from 'react'
import { anomalyAPI, storageAPI } from '../../api'
import { useAuth } from '../../hooks/useAuth'
import type { StorageContainer, TemperatureAnomaly } from '../../types/domain'

interface Props {
  container: StorageContainer | null
  open: boolean
  onClose: () => void
  onChanged: () => void
  onContainerReload: (container: StorageContainer) => void
}

export function AnomalyPanel({ container, open, onClose, onChanged, onContainerReload }: Props) {
  const { user, can } = useAuth()
  const [reportOpen, setReportOpen] = useState(false)
  const [resolveTarget, setResolveTarget] = useState<TemperatureAnomaly | null>(null)
  const [history, setHistory] = useState<TemperatureAnomaly[]>([])
  const [saving, setSaving] = useState(false)
  const [reportForm] = Form.useForm()
  const [resolveForm] = Form.useForm()

  useEffect(() => {
    if (!open || !container) { setHistory([]); return }
    void anomalyAPI.list({ page: 1, pageSize: 20, storageContainerId: container.id })
      .then((result) => setHistory(result.items.filter((item) => item.state !== 'open')))
  }, [open, container?.id])

  const reloadHistory = () => container && anomalyAPI.list({ page: 1, pageSize: 20, storageContainerId: container.id })
    .then((result) => setHistory(result.items.filter((item) => item.state !== 'open')))

  const reloadContainer = async () => {
    if (!container) return
    const reloaded = await storageAPI.get(container.id)
    onContainerReload(reloaded)
  }

  const anomaly = container?.activeAnomaly
  const range = container
    ? (() => {
        const zone = container.temperatureZone
        if (zone === 'minus20') return '-30°C ~ -15°C'
        if (zone === 'liquid_nitrogen') return '-196°C ~ -135°C'
        return '-90°C ~ -65°C'
      })()
    : ''

  const report = async () => {
    if (!container) return
    const values = await reportForm.validateFields()
    setSaving(true)
    try {
      await anomalyAPI.report({
        storageContainerId: container.id,
        recordedC: values.recordedC,
        readingAt: values.readingAt.toISOString(),
        description: values.description || '',
      })
      message.success('冷链异常已登记，容器内已冻存样本已隔离')
      setReportOpen(false)
      reportForm.resetFields()
      await reloadHistory()
      await reloadContainer()
      onChanged()
    } finally { setSaving(false) }
  }

  const resolve = async () => {
    if (!resolveTarget) return
    const values = await resolveForm.validateFields()
    setSaving(true)
    try {
      await anomalyAPI.resolve(resolveTarget.id, {
        decision: values.decision,
        recoveredC: values.decision === 'resolved' ? values.recoveredC : undefined,
        resolutionBasis: values.resolutionBasis,
      })
      message.success(values.decision === 'resolved' ? '异常已解除，样本恢复正常' : '异常已判定为无效，样本解除隔离')
      setResolveTarget(null)
      resolveForm.resetFields()
      await reloadHistory()
      await reloadContainer()
      onChanged()
    } finally { setSaving(false) }
  }

  const selfReported = Boolean(anomaly && user && anomaly.reportedById === user.id)

  return (
    <Drawer
      width={560}
      title={container ? `冷链巡检 · ${container.code}` : '冷链巡检'}
      open={open}
      onClose={onClose}
      extra={can('coldchain:manage') && !anomaly ? (
        <Button type="primary" danger icon={<ExclamationCircleOutlined />} onClick={() => setReportOpen(true)}>登记超限读数</Button>
      ) : undefined}
    >
      {container && (
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <Typography.Paragraph type="secondary">
            {container.name} · 温区范围 {range} · 物理位置 {container.location}
          </Typography.Paragraph>
          {anomaly ? (
            <Alert
              type="error"
              showIcon
              icon={<ExclamationCircleOutlined />}
              style={{ alignItems: 'flex-start' }}
              message={<Space direction="vertical" size={2}><Typography.Text strong>未结异常 {anomaly.anomalyNo}</Typography.Text><Typography.Text>巡检读数 {anomaly.recordedC}°C · {dayjs(anomaly.readingAt).format('YYYY-MM-DD HH:mm')}</Typography.Text><Typography.Text type="secondary">建单人 {anomaly.reportedByName} · 涉及样本 {container.isolatedSpecimenCount} 份</Typography.Text></Space>}
              description={<Space direction="vertical" style={{ marginTop: 12, width: '100%' }}>
                {anomaly.description && <Typography.Text>{anomaly.description}</Typography.Text>}
                <Typography.Text type="secondary">温度恢复不会自动解除隔离，须由非建单人填写依据后处理。</Typography.Text>
                {can('coldchain:manage') && (
                  selfReported
                    ? <Typography.Text type="warning">您是该异常建单人，不能自行解除，请由其他保管员处理。</Typography.Text>
                    : <Button type="primary" icon={<SafetyCertificateOutlined />} onClick={() => { setResolveTarget(anomaly); resolveForm.setFieldsValue({ decision: 'resolved' }) }}>解除隔离 / 判定无效</Button>
                )}
              </Space>}
            />
          ) : (
            <Alert type="success" showIcon message="当前无未结冷链异常" description="巡检发现读数超出温区范围时，由有权限的保管员登记超限读数，系统将自动隔离容器内全部已冻存样本。" />
          )}
          <div>
            <Typography.Title level={5}>异常处理历史</Typography.Title>
            {history.length > 0 ? (
              <Timeline items={history.map((item) => ({
                key: item.id,
                color: item.state === 'resolved' ? 'green' : 'gray',
                children: <div className="timeline-item">
                  <strong>{item.anomalyNo} · {item.state === 'resolved' ? '已解除' : '判定无效'}</strong>
                  <small>超限读数 {item.recordedC}°C（{dayjs(item.readingAt).format('YYYY-MM-DD HH:mm')}） · 建单 {item.reportedByName}</small>
                  {item.recoveredC != null && <small>复核温度 {item.recoveredC}°C · 处理 {item.resolvedByName} · {dayjs(item.resolvedAt).format('YYYY-MM-DD HH:mm')}</small>}
                  <Typography.Paragraph type="secondary" ellipsis={{ rows: 2 }}>{item.resolutionBasis}</Typography.Paragraph>
                </div>,
              }))} />
            ) : <Typography.Text type="secondary">暂无历史记录</Typography.Text>}
          </div>
        </Space>
      )}

      <Drawer title="登记超限读数" width={520} open={reportOpen} onClose={() => setReportOpen(false)}
        extra={<Space><Button onClick={() => setReportOpen(false)}>取消</Button><Button type="primary" danger loading={saving} onClick={() => void report()}>提交并隔离</Button></Space>}>
        <Form form={reportForm} layout="vertical" initialValues={{ readingAt: dayjs() }}>
          <Alert type="warning" showIcon style={{ marginBottom: 16 }} message="同一冻存容器存在未结异常时无法重复建单；提交后容器内已冻存样本立即转为隔离并中止相关交接。" />
          <Row gutter={16}>
            <Col span={12}><Form.Item name="recordedC" label="实测温度 (°C)" rules={[{ required: true, message: '请填写实测温度' }]}><InputNumber min={-200} max={40} precision={1} style={{ width: '100%' }} /></Form.Item></Col>
            <Col span={12}><Form.Item name="readingAt" label="巡检时间" rules={[{ required: true }]}><DatePicker showTime style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Form.Item name="description" label="异常现象/现场说明" rules={[{ required: true, min: 5 }]}><Input.TextArea rows={4} maxLength={1000} showCount placeholder="例如：柜门未关严、压缩机告警，已转移备用温度探头" /></Form.Item>
        </Form>
      </Drawer>

      <Drawer title={resolveTarget ? `处理异常 ${resolveTarget.anomalyNo}` : '处理异常'} width={520} open={Boolean(resolveTarget)} onClose={() => setResolveTarget(null)}
        extra={<Space><Button onClick={() => setResolveTarget(null)}>取消</Button><Button type="primary" loading={saving} onClick={() => void resolve()}>确认提交</Button></Space>}>
        <Form form={resolveForm} layout="vertical">
          <Form.Item name="decision" label="处理决定" rules={[{ required: true }]}>
            <Radio.Group optionType="button" buttonStyle="solid"
              options={[{ value: 'resolved', label: '确认恢复，解除隔离' }, { value: 'invalid', label: '判定无效（误报）' }]} />
          </Form.Item>
          <Form.Item shouldUpdate={(prev, next) => prev.decision !== next.decision} noStyle>
            {({ getFieldValue }) => getFieldValue('decision') !== 'invalid' ? (
              <Form.Item name="recoveredC" label="复核温度 (°C)" rules={[{ required: true, message: '温度恢复须填写复核读数' }]}>
                <InputNumber min={-200} max={40} precision={1} style={{ width: '100%' }} />
              </Form.Item>
            ) : null}
          </Form.Item>
          <Form.Item name="resolutionBasis" label="处理依据" rules={[{ required: true, min: 5, max: 1000 }]}>
            <Input.TextArea rows={5} showCount maxLength={1000} placeholder="温度恢复时长、连续合格读数、探头校准或维修记录、质量负责人确认等" />
          </Form.Item>
          <Alert type="info" showIcon message="提交后将校验复核温度；处理失败时异常和全部样本保持原状。" />
        </Form>
      </Drawer>
    </Drawer>
  )
}
