import { Alert, Descriptions, Drawer, Tabs, Timeline, Typography } from 'antd'
import type { Specimen } from '../../types/domain'
import { formatDateTime } from '../../utils/format'
import { CustodyBadge } from './CustodyBadge'
import { CustodyTimeline } from './CustodyTimeline'
import { QuarantineTag } from './QuarantineTag'
import { StatusBadge } from './StatusBadge'

export function SampleDrawer({ specimen, open, onClose }: { specimen: Specimen | null; open: boolean; onClose: () => void }) {
  return (
    <Drawer width={720} title={specimen ? `样本 ${specimen.accessionNo}` : '样本详情'} open={open} onClose={onClose}>
      {specimen && <Tabs items={[
        { key: 'profile', label: '基本信息', children: <div className="drawer-stack">{specimen.quarantineAnomalyId && <Alert type="error" showIcon message={`冷链异常${specimen.quarantineAnomaly ? ` ${specimen.quarantineAnomaly.anomalyNo}` : ''}隔离中，禁止交接与批准放行`} />}<Descriptions bordered size="small" column={2}><Descriptions.Item label="保管状态"><CustodyBadge state={specimen.state} />{specimen.quarantineAnomalyId && <span style={{ marginLeft: 8 }}><QuarantineTag specimen={specimen} /></span>}</Descriptions.Item><Descriptions.Item label="样本类型">{specimen.sampleType}</Descriptions.Item><Descriptions.Item label="受试者编码">{specimen.subjectCode}</Descriptions.Item><Descriptions.Item label="研究协议">{specimen.protocolCode}</Descriptions.Item><Descriptions.Item label="当前保管人">{specimen.currentCustodian}</Descriptions.Item><Descriptions.Item label="接收时间">{formatDateTime(specimen.receivedAt)}</Descriptions.Item><Descriptions.Item label="体积/分装">{specimen.volumeMl} mL / {specimen.aliquotCount} 份</Descriptions.Item><Descriptions.Item label="有效期">{formatDateTime(specimen.expiresAt)}</Descriptions.Item><Descriptions.Item label="冻存容器">{specimen.storageContainer ? `${specimen.storageContainer.code} · ${specimen.storageContainer.name}` : '待分配'}</Descriptions.Item><Descriptions.Item label="位置">{specimen.position || '-'}</Descriptions.Item><Descriptions.Item label="温区">{specimen.storageContainer ? <StatusBadge value={specimen.storageContainer.temperatureZone} /> : '-'}</Descriptions.Item><Descriptions.Item label="备注" span={2}>{specimen.notes || '-'}</Descriptions.Item></Descriptions></div> },
        { key: 'quarantine', label: `隔离历史 (${specimen.quarantineHistory?.length || 0})`, children: specimen.quarantineHistory?.length ? <Timeline items={specimen.quarantineHistory.map((event) => ({ color: event.action === 'isolated' ? 'red' : 'green', children: <div className="timeline-item"><div><StatusBadge value={event.action} /> <Typography.Text strong>{event.anomaly?.anomalyNo || `异常 #${event.anomalyId}`}</Typography.Text></div><Typography.Text>{event.operatorName} · {formatDateTime(event.createdAt)}</Typography.Text>{event.reason && <Typography.Paragraph type="secondary">{event.reason}</Typography.Paragraph>}</div> }))} /> : <Typography.Text type="secondary">暂无隔离记录</Typography.Text> },
        { key: 'custody', label: `交接链 (${specimen.transfers?.length || 0})`, children: <CustodyTimeline transfers={specimen.transfers || []} /> },
        { key: 'protocol', label: `协议复核 (${specimen.protocolReviews?.length || 0})`, children: specimen.protocolReviews?.length ? specimen.protocolReviews.map((review) => <div className="review-record" key={review.id}><div><StatusBadge value={review.decision} /> <Typography.Text strong>{review.protocolCode}</Typography.Text></div><Typography.Text>{review.reviewerName} · {formatDateTime(review.reviewedAt)}</Typography.Text><Typography.Paragraph type="secondary">{review.notes}</Typography.Paragraph></div>) : <Typography.Text type="secondary">暂无协议复核记录</Typography.Text> },
      ]} />}
    </Drawer>
  )
}
