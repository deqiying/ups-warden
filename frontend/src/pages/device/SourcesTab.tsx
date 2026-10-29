import { useMutation, useQuery } from "@tanstack/react-query";
import { getSourceDiagnostics, testSource } from "../../api/devices.ts";
import { isApiError } from "../../api/client.ts";
import { normalizeField } from "../../api/normalize.ts";
import { queryKeys, useDeviceSources, useTimeZone } from "../../api/hooks.ts";
import type { ProbeResult, Source, SourceDiagnostics, TraceEntry } from "../../api/types.ts";
import { Badge, type BadgeTone } from "../../components/Badge.tsx";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { protocolLabel } from "../../model/device.ts";
import { fieldText, NA_TEXT } from "../../model/format.ts";
import { formatFullDateTime, formatTime } from "../../model/time.ts";

/** 数据源运行状态随采集变化；停留在本页时定期刷新。 */
const STATUS_REFRESH_MS = 10_000;

const QUALITY_TONE: Readonly<Record<string, BadgeTone>> = {
  good: "green",
  invalid: "yellow",
  stale: "orange",
  unsupported: "gray",
};

/** 运行状态错误码文案；password_wrong 为 AYI9 设备密码错误（103）。 */
const SOURCE_ERROR_TEXT: Readonly<Record<string, string>> = {
  password_wrong: "设备密码错误（103）",
  timeout: "超时",
};

function credentialText(source: Source): string {
  if (source.protocol === "snmp") {
    return source.community_set ? "Community 已设置" : "Community 未设置 · 驱动默认 public";
  }
  if (source.protocol === "ayi9-json") {
    return source.password_set ? "密码已设置" : "密码未设置 · 使用空密码";
  }
  return "无需凭据";
}

function paramsText(source: Source): string | null {
  if (source.protocol === "modbus" && typeof source.params?.unit_id === "number") {
    return `Unit ID ${source.params.unit_id}`;
  }
  return null;
}

/** Trace 的原始值 / 解码值是诊断信息，按 JSON 文本显示。 */
function rawText(value: unknown): string {
  if (value === undefined || value === null) return "";
  return JSON.stringify(value);
}

function traceResult(entry: TraceEntry): string {
  if (!entry.final) return NA_TEXT;
  return fieldText(entry.field, normalizeField(entry.final));
}

/** 浏览器内下载诊断 JSON；内容已由后端过滤凭据。 */
function downloadJson(filename: string, data: unknown): void {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}

/** 设备详情「数据源与诊断」（F2-5）。 */
export function SourcesTab({ deviceId }: { deviceId: number }) {
  const sources = useDeviceSources(deviceId, STATUS_REFRESH_MS);
  if (sources.isPending) {
    return (
      <div className="card mt-4">
        <Empty title="正在加载数据源…">
          <Spinner />
        </Empty>
      </div>
    );
  }
  if (sources.isError) {
    return (
      <Banner
        className="mt-4"
        tone="critical"
        role="alert"
        title="数据源加载失败"
        actions={
          <Button size="sm" onClick={() => void sources.refetch()}>
            重试
          </Button>
        }
      >
        {sources.error.message}
      </Banner>
    );
  }
  if (sources.data.length === 0) {
    return (
      <div className="card mt-4">
        <Empty icon="antenna" tone="info" title="该设备还没有数据源">
          添加数据源后才会采集数据；设备与数据源的编辑在「设备」页面进行。
        </Empty>
      </div>
    );
  }
  return (
    <div className="mt-4 vstack gap-4">
      {sources.data.map((source) => (
        <SourcePanel key={source.id} source={source} />
      ))}
    </div>
  );
}

function SourcePanel({ source }: { source: Source }) {
  const timeZone = useTimeZone();
  const test = useMutation<ProbeResult>({ mutationFn: () => testSource(source.id) });
  const diagnostics = useQuery<SourceDiagnostics | null>({
    queryKey: [...queryKeys.sources, "raw", source.id],
    queryFn: async ({ signal }) => {
      try {
        return await getSourceDiagnostics(source.id, signal);
      } catch (err) {
        // 404：暂无内存诊断（尚未采集或服务刚重启），按空态显示而不是错误。
        if (isApiError(err) && err.status === 404) return null;
        throw err;
      }
    },
  });
  const status = source.status;
  const extra = paramsText(source);

  return (
    <>
      <div className="split">
        <div className="vstack gap-4">
          <div>
            <div className="list-header">数据源{source.enabled ? "" : "（已停用）"}</div>
            <div className="list">
              <div className="row form-row">
                <span className="row-main">协议</span>
                <span className="row-value">{protocolLabel(source)}</span>
              </div>
              <div className="row form-row">
                <span className="row-main">地址</span>
                <span className="row-value mono">
                  {source.host}:{source.port}
                </span>
              </div>
              <div className="row form-row">
                <span className="row-main">凭据</span>
                <span className="row-value">{credentialText(source)}</span>
              </div>
              {extra ? (
                <div className="row form-row">
                  <span className="row-main">参数</span>
                  <span className="row-value">{extra}</span>
                </div>
              ) : null}
              <div className="row form-row">
                <span className="row-main">Profile</span>
                <span className="row-value">
                  {source.profile_key} · v{source.profile_version}
                </span>
              </div>
              <div className="row form-row">
                <span className="row-main">轮询 / 超时 / 重试</span>
                <span className="row-value">
                  {source.poll_ms / 1000} s / {source.timeout_ms / 1000} s / {source.retries}
                </span>
              </div>
            </div>
          </div>
          <div>
            <div className="list-header">运行状态</div>
            <div className="list">
              {status ? (
                <>
                  <div className="row form-row">
                    <span className="row-main">最后成功</span>
                    <span className={status.failures > 0 ? "row-value ink-orange" : "row-value"}>
                      {status.last_ok_at ? formatTime(status.last_ok_at, timeZone) : "从未成功"}
                    </span>
                  </div>
                  <div className="row form-row">
                    <span className="row-main">延迟</span>
                    <span className="row-value">{status.latency_ms} ms</span>
                  </div>
                  <div className="row form-row">
                    <span className="row-main">连续失败</span>
                    <span className={status.failures > 0 ? "row-value ink-orange" : "row-value"}>
                      {status.failures}
                      {status.error_code
                        ? ` · ${SOURCE_ERROR_TEXT[status.error_code] ?? status.error_code}`
                        : ""}
                    </span>
                  </div>
                </>
              ) : (
                <div className="row form-row">
                  <span className="row-main muted">没有活动的采集任务（设备或数据源已停用）</span>
                </div>
              )}
            </div>
          </div>
          <div className="hstack">
            <Button
              variant="tinted"
              icon="refresh"
              disabled={test.isPending}
              onClick={() => test.mutate()}
            >
              {test.isPending ? "正在测试…" : "只读连接测试"}
            </Button>
          </div>
          {test.isError ? (
            <Banner tone="critical" role="alert" title="连接测试未成功">
              {test.error.message}
            </Banner>
          ) : null}
          {test.isSuccess ? (
            <Banner tone="info" role="status" icon="check-circle" title="连接测试成功">
              延迟 {test.data.latency_ms} ms
              {[test.data.device_info?.model, test.data.device_info?.firmware]
                .filter(Boolean)
                .map((v) => ` · ${v}`)
                .join("")}
            </Banner>
          ) : null}
          <p className="list-caption" style={{ paddingTop: 0 }}>
            连接测试只发送白名单内的读请求，与正常采集共用同一地址的串行通道。
          </p>
        </div>
        <TraceCard
          source={source}
          diagnostics={diagnostics.data}
          pending={diagnostics.isPending}
          error={diagnostics.isError ? diagnostics.error.message : null}
          onRetry={() => void diagnostics.refetch()}
          timeZone={timeZone}
        />
      </div>
      {source.protocol === "snmp" && !source.community_set ? (
        <Banner
          tone="neutral"
          icon="shield"
          title="安全提示：未设置 Community，正在使用默认值 public"
        >
          建议在 UPS 网卡中改用非默认 Community，并在数据源中填写。UPS Warden
          只读取数据，不会向设备发送任何写入指令。
        </Banner>
      ) : null}
    </>
  );
}

function TraceCard({
  source,
  diagnostics,
  pending,
  error,
  onRetry,
  timeZone,
}: {
  source: Source;
  diagnostics: SourceDiagnostics | null | undefined;
  pending: boolean;
  error: string | null;
  onRetry: () => void;
  timeZone: string | undefined;
}) {
  const trace = diagnostics?.trace ?? [];
  return (
    <section className="card" style={{ overflow: "hidden" }}>
      <div className="card-head">
        <div>
          <h3 className="card-title">字段追踪</h3>
          <div className="card-sub">
            {diagnostics
              ? `最近一次采集 ${formatFullDateTime(diagnostics.raw?.at, timeZone)} · 仅保存在内存，用于校准与诊断`
              : "仅保存在内存，用于校准与诊断"}
          </div>
        </div>
        <div className="hstack">
          <Button size="sm" icon="refresh" onClick={onRetry} aria-label="刷新字段追踪" iconOnly />
          <Button
            size="sm"
            icon="download"
            disabled={!diagnostics}
            onClick={() =>
              diagnostics && downloadJson(`source-${source.id}-diagnostics.json`, diagnostics)
            }
          >
            原始数据
          </Button>
        </div>
      </div>
      {pending ? (
        <Empty title="正在加载诊断数据…">
          <Spinner />
        </Empty>
      ) : error ? (
        <div className="card-body">
          <Banner
            tone="critical"
            role="alert"
            title="诊断数据加载失败"
            actions={
              <Button size="sm" onClick={onRetry}>
                重试
              </Button>
            }
          >
            {error}
          </Banner>
        </div>
      ) : !diagnostics ? (
        <Empty icon="info" tone="info" title="暂无诊断数据">
          尚未完成采集，或服务重启后还没有新的采集结果。
        </Empty>
      ) : trace.length === 0 ? (
        <Empty icon="info" tone="info" title="最近一次采集没有字段追踪" />
      ) : (
        <div style={{ overflowX: "auto" }} className="mt-3">
          <table className="table trace">
            <thead>
              <tr>
                <th>统一字段</th>
                <th>质量</th>
                <th>来源</th>
                <th>原始 → 解码</th>
                <th className="num">结果</th>
              </tr>
            </thead>
            <tbody>
              {trace.map((entry) => {
                const raw = rawText(entry.raw);
                const decoded = rawText(entry.decoded);
                const result = traceResult(entry);
                return (
                  <tr key={entry.field}>
                    <td>{entry.field}</td>
                    <td>
                      <Badge tone={QUALITY_TONE[entry.quality] ?? "gray"}>{entry.quality}</Badge>
                    </td>
                    <td className={entry.source ? undefined : "faint"}>
                      {entry.source || NA_TEXT}
                    </td>
                    <td>
                      {raw || decoded
                        ? `${raw || NA_TEXT} → ${decoded || NA_TEXT}`
                        : (entry.reason ?? "")}
                      {entry.reason && (raw || decoded) ? ` · ${entry.reason}` : ""}
                    </td>
                    <td className={result === NA_TEXT ? "num na" : "num"}>{result}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
