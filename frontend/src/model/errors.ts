/*
 * 后端稳定错误码 → 界面中文文案。
 *
 * 来源：docs/reference/*.md 与 backend/internal/api 中的 fail(...) 调用。未收录的 code 回退显示后端 message，
 * 后端 message 也为空时显示通用文案，不把失败显示为成功。
 */
export const ERROR_MESSAGES: Readonly<Record<string, string>> = {
  // 通用与认证
  unauthorized: "请先登录",
  invalid_credentials: "用户名或密码错误，或会话已失效",
  rate_limited: "请求过于频繁，请稍后重试",
  origin_rejected: "请求来源不匹配，请从同一地址访问",
  csrf_invalid: "页面会话已失效，请刷新后重试",
  forbidden: "需要管理员权限",
  invalid_json: "请求格式无效",
  json_required: "请求格式无效（需要 JSON）",
  body_too_large: "请求内容超过大小限制",
  validation_failed: "配置校验失败",
  resource_conflict: "资源被引用、名称冲突或存在约束冲突",
  confirmation_required: "需要确认后才能执行",
  not_found: "接口不存在",
  internal_error: "操作失败，请检查服务日志",
  encoding_error: "响应编码失败，请检查服务日志",
  invalid_id: "ID 无效",
  invalid_cursor: "分页游标无效",
  invalid_limit: "分页数量超出范围",
  invalid_filter: "筛选条件无效",
  invalid_time_range: "时间范围无效",
  configuration_saved_runtime_error: "配置已保存，但运行恢复失败",
  // 初始化与用户
  setup_closed: "初始化已完成，请直接登录",
  invalid_setup_token: "初始化令牌无效",
  user_conflict: "用户名冲突，或不能删除该管理员",
  user_not_found: "用户不存在",
  // 设备、数据源与 Profile
  device_not_found: "设备不存在",
  source_not_found: "数据源不存在",
  profile_not_found: "Profile 不存在",
  profile_invalid: "Profile 无效",
  builtin_readonly: "内置 Profile 只读，请复制后修改",
  enabled_required: "需要指定启用状态",
  snapshot_unavailable: "设备暂无运行快照",
  diagnostic_unavailable: "暂无诊断数据",
  source_busy: "设备正忙（同一地址的采集或测试正在进行），请稍后重试",
  source_password_wrong: "设备密码错误（103）",
  probe_failed: "连接测试失败",
  invalid_history_field: "所选字段不支持历史查询",
  invalid_resolution: "历史分辨率无效",
  invalid_event_type: "事件类型无效",
  invalid_severity: "严重度无效",
  // 通知
  channel_not_found: "通知渠道不存在",
  channel_disabled: "请先启用渠道再发送测试消息",
  notification_queue_full: "渠道发送队列已满，请稍后重试",
  notification_unavailable: "通知渠道不可用或正在变更",
  // 规则、动作与主机
  rule_not_found: "规则不存在",
  action_not_found: "动作不存在",
  action_busy: "动作正在执行，不能修改或删除",
  action_unavailable: "动作服务已关闭",
  plan_confirmation_required: "plan 动作不能手动执行，请在关机计划中输入计划名称后执行",
  host_not_found: "主机不存在",
  host_changed: "获取指纹期间地址或端口已变化，请重新获取",
  hostkey_candidate_invalid: "指纹已过期或不匹配，请重新获取后在 5 分钟内确认",
  hostkey_unconfirmed: "主机指纹尚未确认，无法连接",
  hostkey_mismatch: "主机指纹与已确认的不一致，已拒绝连接",
  host_timeout: "主机操作超时",
  host_cancelled: "主机操作已取消",
  host_operation_failed: "主机操作失败",
  // 关机计划
  plan_not_found: "计划不存在",
  plan_run_not_found: "运行记录不存在",
  plan_run_active: "该计划已有倒计时或执行中的运行",
  plan_cycle_executed: "该计划已在本停电周期真实执行过",
  plan_cycle_ended: "设备的停电周期已结束，请刷新后重试",
  plan_run_finished: "运行已结束",
  plan_unavailable: "计划服务已关闭",
  // 前端自身（非后端 code）
  network_error: "无法连接服务器，请检查网络或服务状态",
  invalid_response: "服务器响应格式无效",
};
// 这些 code 的后端 message 携带具体原因（如“倒计时需在 0～3600 秒”），且不含凭据，优先展示原文。
const SERVER_DETAIL_CODES = new Set([
  "validation_failed",
  "host_operation_failed",
  "profile_invalid",
]);

/** 按 code 取文案；未知 code 回退后端 message，再回退通用文案（含 HTTP 状态便于诊断）。 */
export function errorMessage(code: string, serverMessage: string, status: number): string {
  if (serverMessage && SERVER_DETAIL_CODES.has(code)) return serverMessage;
  // 只认自有键：constructor、toString 等原型属性不是已知 code。
  const known = Object.hasOwn(ERROR_MESSAGES, code) ? ERROR_MESSAGES[code] : undefined;
  if (known) return known;
  if (serverMessage) return serverMessage;
  return status > 0 ? `请求失败（HTTP ${status}）` : "请求失败";
}
