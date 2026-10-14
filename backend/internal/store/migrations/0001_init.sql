-- 所有时间列使用 UTC Unix 毫秒 INTEGER；可空时间表示尚未发生。
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    applied_at INTEGER NOT NULL
);
CREATE TABLE settings (
    key TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL CHECK (json_valid(value))
);
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'admin',
    created_at INTEGER NOT NULL,
    last_login_at INTEGER
);
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE devices (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    location TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    config TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config))
);
CREATE TABLE profiles (
    key TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
-- profile_key 同时可指向 embed 内置 Profile，不能直接外键到自定义 profiles。
CREATE TABLE sources (
    id INTEGER PRIMARY KEY,
    device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    protocol TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    params TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(params)),
    secret BLOB,
    profile_key TEXT NOT NULL,
    profile_version INTEGER NOT NULL CHECK (profile_version > 0),
    poll_ms INTEGER NOT NULL CHECK (poll_ms > 0),
    timeout_ms INTEGER NOT NULL CHECK (timeout_ms > 0),
    retries INTEGER NOT NULL DEFAULT 0 CHECK (retries >= 0),
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);
CREATE INDEX sources_device ON sources(device_id);
CREATE TABLE outage_cycles (
    id INTEGER PRIMARY KEY,
    device_id INTEGER NOT NULL REFERENCES devices(id),
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    UNIQUE (id, device_id),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE INDEX outage_cycles_device_started ON outage_cycles(device_id, started_at);
CREATE TABLE device_state (
    device_id INTEGER PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    power_state TEXT NOT NULL,
    power_state_raw TEXT NOT NULL,
    pending_since INTEGER,
    on_battery_since INTEGER,
    outage_cycle_id INTEGER,
    comm_state TEXT NOT NULL,
    last_ok_at INTEGER,
    alert_flags TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(alert_flags)),
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (outage_cycle_id, device_id) REFERENCES outage_cycles(id, device_id)
);
CREATE INDEX device_state_cycle ON device_state(outage_cycle_id, device_id);
CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    device_id INTEGER NOT NULL REFERENCES devices(id),
    type TEXT NOT NULL,
    severity TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    cleared_at INTEGER,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    summary TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(summary)),
    acked_at INTEGER,
    acked_by INTEGER REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX events_device_started ON events(device_id, started_at);
CREATE INDEX events_active ON events(active);
CREATE INDEX events_acked_by ON events(acked_by);
CREATE TABLE history_sample (
    device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    ts INTEGER NOT NULL,
    data TEXT NOT NULL CHECK (json_valid(data)),
    PRIMARY KEY (device_id, ts)
);
CREATE INDEX history_sample_ts ON history_sample(ts);
CREATE TABLE history_1m (
    device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    bucket INTEGER NOT NULL CHECK (bucket % 60000 = 0),
    data TEXT NOT NULL CHECK (json_valid(data)),
    PRIMARY KEY (device_id, bucket)
);
CREATE INDEX history_1m_bucket ON history_1m(bucket);
CREATE TABLE rules (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    dry_run INTEGER NOT NULL DEFAULT 1 CHECK (dry_run IN (0, 1)),
    scope TEXT NOT NULL CHECK (json_valid(scope)),
    trigger TEXT NOT NULL CHECK (json_valid(trigger)),
    actions TEXT NOT NULL CHECK (json_valid(actions)),
    recovery_actions TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(recovery_actions)),
    cooldown_s INTEGER NOT NULL DEFAULT 0 CHECK (cooldown_s >= 0)
);
CREATE TABLE rule_state (
    rule_id INTEGER NOT NULL REFERENCES rules(id) ON DELETE CASCADE,
    device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    fired INTEGER NOT NULL DEFAULT 0 CHECK (fired IN (0, 1)),
    fired_at INTEGER,
    since TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(since)),
    PRIMARY KEY (rule_id, device_id)
);
CREATE INDEX rule_state_device ON rule_state(device_id);
CREATE TABLE rule_logs (
    id INTEGER PRIMARY KEY,
    rule_id INTEGER NOT NULL REFERENCES rules(id),
    device_id INTEGER NOT NULL REFERENCES devices(id),
    at INTEGER NOT NULL,
    kind TEXT NOT NULL,
    reason TEXT NOT NULL,
    "values" TEXT NOT NULL DEFAULT '{}' CHECK (json_valid("values")),
    action_run_ids TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(action_run_ids))
);
CREATE INDEX rule_logs_rule_at ON rule_logs(rule_id, at);
CREATE INDEX rule_logs_device_at ON rule_logs(device_id, at);
CREATE INDEX rule_logs_at ON rule_logs(at);
CREATE TABLE channels (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    config TEXT NOT NULL CHECK (json_valid(config)),
    secret BLOB,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))
);
CREATE TABLE notify_templates (
    event_type TEXT NOT NULL,
    format TEXT NOT NULL,
    body TEXT NOT NULL,
    PRIMARY KEY (event_type, format)
);
CREATE TABLE notify_logs (
    id INTEGER PRIMARY KEY,
    channel_id INTEGER NOT NULL REFERENCES channels(id),
    event_id INTEGER REFERENCES events(id),
    at INTEGER NOT NULL,
    summary TEXT NOT NULL,
    ok INTEGER NOT NULL CHECK (ok IN (0, 1)),
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX notify_logs_channel_at ON notify_logs(channel_id, at);
CREATE INDEX notify_logs_event ON notify_logs(event_id);
CREATE INDEX notify_logs_at ON notify_logs(at);
CREATE TABLE hosts (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    kind TEXT NOT NULL,
    conn TEXT NOT NULL CHECK (json_valid(conn)),
    secret BLOB,
    hostkey_fingerprint TEXT,
    online_check TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(online_check))
);
CREATE TABLE actions (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    config TEXT NOT NULL CHECK (json_valid(config)),
    secret BLOB,
    timeout_s INTEGER NOT NULL CHECK (timeout_s > 0)
);
CREATE TABLE action_runs (
    id INTEGER PRIMARY KEY,
    action_id INTEGER NOT NULL REFERENCES actions(id),
    trigger TEXT NOT NULL CHECK (json_valid(trigger)),
    dry_run INTEGER NOT NULL CHECK (dry_run IN (0, 1)),
    status TEXT NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    exit_code INTEGER,
    stdout TEXT NOT NULL DEFAULT '',
    stderr TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX action_runs_action_started ON action_runs(action_id, started_at);
CREATE INDEX action_runs_started ON action_runs(started_at);
CREATE INDEX action_runs_status ON action_runs(status);
CREATE TABLE plans (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    config TEXT NOT NULL CHECK (json_valid(config))
);
CREATE TABLE plan_runs (
    id INTEGER PRIMARY KEY,
    plan_id INTEGER NOT NULL REFERENCES plans(id),
    device_id INTEGER REFERENCES devices(id),
    outage_cycle_id INTEGER REFERENCES outage_cycles(id),
    dry_run INTEGER NOT NULL CHECK (dry_run IN (0, 1)),
    status TEXT NOT NULL CHECK (status IN ('countdown', 'running', 'completed', 'completed_with_errors', 'cancelled', 'aborted')),
    trigger TEXT NOT NULL CHECK (json_valid(trigger)),
    countdown_until INTEGER,
    current_stage INTEGER NOT NULL DEFAULT 0 CHECK (current_stage >= 0),
    plan_snapshot TEXT NOT NULL CHECK (json_valid(plan_snapshot)),
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    CHECK (outage_cycle_id IS NULL OR device_id IS NOT NULL),
    FOREIGN KEY (outage_cycle_id, device_id) REFERENCES outage_cycles(id, device_id)
);
-- 同一停电周期的真实执行只能发生一次；无周期的手动执行不占用此约束。
CREATE UNIQUE INDEX plan_runs_real_cycle ON plan_runs(plan_id, outage_cycle_id)
    WHERE dry_run = 0 AND outage_cycle_id IS NOT NULL;
-- 同一计划最多一个活动运行，包含 dry-run；防止演练与真实执行并发。
CREATE UNIQUE INDEX plan_runs_active ON plan_runs(plan_id)
    WHERE status IN ('countdown', 'running');
CREATE INDEX plan_runs_device ON plan_runs(device_id);
CREATE INDEX plan_runs_cycle ON plan_runs(outage_cycle_id, device_id);
CREATE INDEX plan_runs_status ON plan_runs(status);
CREATE INDEX plan_runs_started ON plan_runs(started_at);
CREATE TABLE plan_run_steps (
    run_id INTEGER NOT NULL REFERENCES plan_runs(id) ON DELETE CASCADE,
    stage INTEGER NOT NULL CHECK (stage >= 0),
    step INTEGER NOT NULL CHECK (step >= 0),
    action_id INTEGER REFERENCES actions(id),
    host_id INTEGER REFERENCES hosts(id),
    status TEXT NOT NULL CHECK (status IN ('waiting', 'running', 'sent', 'offline', 'failed', 'cancelled', 'unknown')),
    started_at INTEGER,
    ended_at INTEGER,
    result TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(result)),
    PRIMARY KEY (run_id, stage, step)
);
CREATE INDEX plan_run_steps_action ON plan_run_steps(action_id);
CREATE INDEX plan_run_steps_host ON plan_run_steps(host_id);
CREATE TABLE audit_logs (
    id INTEGER PRIMARY KEY,
    at INTEGER NOT NULL,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    ip TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    target TEXT NOT NULL,
    diff TEXT NOT NULL CHECK (json_valid(diff)),
    result TEXT NOT NULL
);
CREATE INDEX audit_logs_at ON audit_logs(at);
CREATE INDEX audit_logs_user_at ON audit_logs(user_id, at);
