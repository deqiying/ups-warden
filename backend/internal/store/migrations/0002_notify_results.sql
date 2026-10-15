-- 同一渠道的多个接收目标独立重试并分别保留结果；旧日志保持兼容。
ALTER TABLE notify_logs ADD COLUMN target TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_logs ADD COLUMN attempts INTEGER NOT NULL DEFAULT 1 CHECK (attempts BETWEEN 0 AND 4);
ALTER TABLE notify_logs ADD COLUMN template_fallback INTEGER NOT NULL DEFAULT 0 CHECK (template_fallback IN (0, 1));
