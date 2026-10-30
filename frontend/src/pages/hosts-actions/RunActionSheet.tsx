import { useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { useNavigate } from "react-router";
import { buildActionRunBody, runAction, type Action } from "../../api/actions.ts";
import { queryKeys, useChannels, useDevices, useHosts } from "../../api/hooks.ts";
import {
  Banner,
  Button,
  Dialog,
  Segmented,
  Select,
  Sheet,
  useToast,
} from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { useLive } from "../../live/hooks.ts";
import { failureText, runPreviewLines } from "./model.ts";

type Mode = "dry" | "real";

/**
 * 手动执行动作（F5-5）。
 * - 默认 dry-run；请求体总是带 `confirm:true` 与显式 `dry_run`（未传 dry_run 会被后端当作真实执行）；
 * - 切换到真实执行时显示目标与命令摘要、改变按钮文案，并在提交前再确认一次（Q-P1）；
 * - plan 动作不能手动执行（列表中不提供入口；后端仍会返回 422 plan_confirmation_required）。
 */
export function RunActionSheet({ action, onClose }: { action: Action; onClose: () => void }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const ids = useId();
  const [mode, setMode] = useState<Mode>("dry");
  const [deviceId, setDeviceId] = useState<number | null>(null);
  const [confirmReal, setConfirmReal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);

  const devices = useDevices().data ?? [];
  const states = useLive((s) => s.data.devices);
  const hosts = useHosts().data;
  const channels = useChannels().data;
  const channelName = (id: number) => channels?.find((c) => c.id === id)?.name ?? `渠道 #${id}`;
  const preview = runPreviewLines(action, { hosts, channelName });
  const host =
    action.type === "ssh" ? hosts?.find((h) => h.id === action.config.host_id) : undefined;
  const unconfirmedHost = action.type === "ssh" && host !== undefined && !host.hostkey_fingerprint;

  const submit = async (dryRun: boolean) => {
    setBusy(true);
    setFailure(null);
    try {
      const res = await runAction(action.id, buildActionRunBody({ dryRun, deviceId }));
      void queryClient.invalidateQueries({ queryKey: queryKeys.actionRuns });
      toast.show({
        tone: "info",
        title: dryRun ? "已提交 dry-run" : "已提交执行",
        message: `运行 #${res.run_id} 已受理。受理不代表成功，结果见执行记录。`,
        duration: 10_000,
        actions: [
          {
            label: "查看执行记录",
            onClick: () =>
              void navigate(`/hosts-actions?tab=runs&action=${action.id}&open=${res.run_id}`),
          },
        ],
      });
      onClose();
    } catch (err) {
      // 409 snapshot_unavailable / 422 plan_confirmation_required / 503 action_unavailable
      setFailure(failureText(err));
      setBusy(false);
    }
  };

  const deviceOptions = [
    { value: "", label: "不指定设备" },
    ...devices.map((d) => {
      const hasSnapshot = states.some((s) => s.device_id === d.id);
      return {
        value: String(d.id),
        label: hasSnapshot ? `${d.name} · 当前快照` : `${d.name} · 暂无快照`,
        disabled: !hasSnapshot,
      };
    }),
  ];

  const dry = mode === "dry";

  return (
    <>
      <Sheet
        open
        onClose={onClose}
        title={`运行「${action.name}」`}
        width="min(600px, calc(100vw - 32px))"
        dismissible={!busy}
        trailing={
          dry ? (
            <Button
              variant="primary"
              size="sm"
              className="ha-btn-indigo"
              disabled={busy}
              aria-busy={busy}
              onClick={() => void submit(true)}
            >
              {busy ? "正在提交…" : "运行 dry-run"}
            </Button>
          ) : (
            <Button
              variant="danger"
              size="sm"
              disabled={busy}
              aria-busy={busy}
              onClick={() => setConfirmReal(true)}
            >
              {busy ? "正在提交…" : "真实执行…"}
            </Button>
          )
        }
      >
        <div className="form-grid">
          <span className="form-label">方式</span>
          <div>
            <Segmented<Mode>
              aria-label="运行方式"
              options={[
                { value: "dry", label: "Dry-run 预演", icon: "flask" },
                { value: "real", label: "真实执行" },
              ]}
              value={mode}
              onChange={setMode}
            />
            <div className="hint">
              {dry
                ? "Dry-run 只保存执行记录和脱敏预览，不连接主机、不发送请求、不启动脚本、不入通知队列。"
                : "真实执行会立即对下方目标生效。"}
            </div>
          </div>
          <label htmlFor={`${ids}-dev`}>设备上下文</label>
          <div>
            <Select
              id={`${ids}-dev`}
              options={deviceOptions}
              value={deviceId === null ? "" : String(deviceId)}
              onChange={(v) => setDeviceId(v ? Number(v) : null)}
            />
            <div className="hint">用于填充 UPS_DEVICE_NAME 等变量；可不指定。</div>
          </div>
        </div>

        {preview.length > 0 ? (
          <div className="ha-preview mt-6">
            <div className="head">
              <Icon name="eye" />
              将要执行
            </div>
            <dl className="ha-preview-body">
              {preview.map((line) => (
                <div key={line.label}>
                  <dt>{line.label}</dt>
                  <dd className="mono">{line.value}</dd>
                </div>
              ))}
            </dl>
          </div>
        ) : null}

        {unconfirmedHost ? (
          <Banner tone="warning" role="status" className="mt-4" title="主机指纹未确认">
            真实执行会被拒绝。请先在「受控主机」中获取并确认指纹。
          </Banner>
        ) : null}
        {!dry ? (
          <Banner tone="warning" icon="power" className="mt-4">
            选择「真实执行」会立即生效
            {action.type === "ssh" && action.config.shutdown ? "，可能关闭目标主机" : ""}
            。同一动作不会并发执行；执行期间不能修改或删除该动作。
          </Banner>
        ) : null}
        {failure ? (
          <Banner tone="critical" role="alert" title="未能提交" className="mt-4">
            {failure}
          </Banner>
        ) : null}
      </Sheet>
      <Dialog
        open={confirmReal}
        onClose={() => setConfirmReal(false)}
        title={`真实执行「${action.name}」？`}
        message={
          preview.length > 0
            ? preview.map((l) => `${l.label}：${l.value}`).join("；")
            : "将立即执行该动作。"
        }
        icon="power"
        tone="danger"
        actions={
          <>
            <Button onClick={() => setConfirmReal(false)}>取消</Button>
            <Button
              variant="danger"
              onClick={() => {
                setConfirmReal(false);
                void submit(false);
              }}
            >
              真实执行
            </Button>
          </>
        }
      />
    </>
  );
}
