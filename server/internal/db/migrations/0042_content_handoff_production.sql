-- HUI-3553 MX-OPS R2 增量:制作交接增加执行偏好与权威队列投影(只读消费)。
-- 纪律:mx-ops-c0/v1 只读;保存的是合同 job_queue_view 的公开投影(已剥离
-- internal_debug),未知=null、0=有效测量值;不改旧列语义、不回填历史事实。
-- 旧行兼容:全部 ADD COLUMN NOT NULL DEFAULT(旧交接=immediate/无队列投影)。
ALTER TABLE content_handoffs ADD COLUMN execution_preference TEXT NOT NULL DEFAULT '';
ALTER TABLE content_handoffs ADD COLUMN policy_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE content_handoffs ADD COLUMN policy_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE content_handoffs ADD COLUMN job_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE content_handoffs ADD COLUMN job_queue_view TEXT NOT NULL DEFAULT '';
