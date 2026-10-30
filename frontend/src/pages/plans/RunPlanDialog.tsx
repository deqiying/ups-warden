import { useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { queryKeys, useDevices } from "../../api/hooks.ts";
import { buildPlanRunBody, runPlan, type Plan } from "../../api/plans.ts";
import {
  ConfirmNameDialog,
  Segmented,
  Select,
  type SegmentedOption,
  useToast,
} from "../../components/index.ts";
import { useLive } from "../../live/hooks.ts";
import { planStats } from "./model.ts";

type Mode = "dry" | "real";

const MODE_OPTIONS: SegmentedOption<Mode>[] = [
  { value: "dry", label: "Dry-run 预演", icon: "flask" },
  { value: "real", label: "真实执行" },
];

export interface RunPlanDialogProps {
  plan: Plan;
  open: boolean;
  onClose: () => void;
  /** 202 受理后调用（运行已创建，不代表成功）。 */
  onStarted: (runId: number) => void;
}

/**
 * 手动执行计划（F6-2）。
 * - 必须输入与计划名称完全一致的名称（不预填、不 trim，dry-run 也不例外）；
 * - 默认 dry-run，请求体总是显式提交 `dry_run`（后端未传按真实执行处理）；
 * - 可选关联设备：使用该设备的运行快照，处于停电周期时市电恢复会取消运行，
 *   且同一周期只真实执行一次。
 * 409 plan_run_active / plan_cycle_executed / plan_cycle_ended / snapshot_unavailable
 * 与 422 confirmation_required 由 ConfirmNameDialog 在框内显示。
 */
export function RunPlanDialog({ plan, open, onClose, onStarted }: RunPlanDialogProps) {
  if (!open) return null;
  return <RunPlanDialogBody plan={plan} onClose={onClose} onStarted={onStarted} />;
}

function RunPlanDialogBody({ plan, onClose, onStarted }: Omit<RunPlanDialogProps, "open">) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const ids = useId();
  const [mode, setMode] = useState<Mode>("dry");
  const [deviceId, setDeviceId] = useState<number | null>(null);
  const devices = useDevices().data ?? [];
  const states = useLive((s) => s.data.devices);
  const dry = mode === "dry";
  const stats = planStats(plan);

  const deviceOptions = [
    { value: "", label: "不关联设备" },
    ...devices.map((d) => {
      const state = states.find((s) => s.device_id === d.id);
      const label = !state
        ? `${d.name} · 暂无快照`
        : state.outage_cycle_id !== null
          ? `${d.name} · 停电周期 #${state.outage_cycle_id}`
          : `${d.name} · 当前快照`;
      return { value: String(d.id), label, disabled: !state };
    }),
  ];

  const message = dry
    ? "Dry-run 只为每个步骤写入预览记录：不连接主机、不等待离线、不发送计划通知。"
    : `真实执行会按 ${stats.stages} 个阶段依次执行 ${stats.steps} 个步骤${
        stats.selfShutdown ? "，最后关闭 UPS Warden 所在主机" : ""
      }。已发出的命令无法撤回；计划不会向 UPS 发送任何指令。`;

  return (
    <ConfirmNameDialog
      open
      onClose={onClose}
      title={`手动执行「${plan.name}」`}
      message={message}
      name={plan.name}
      label={`输入计划名称「${plan.name}」以确认`}
      placeholder="计划名称"
      icon={dry ? "flask" : "power"}
      tone={dry ? "info" : "danger"}
      confirmLabel={dry ? "执行 dry-run" : "真实执行"}
      onConfirm={async (typed) => {
        const res = await runPlan(
          plan.id,
          buildPlanRunBody({ confirmName: typed, dryRun: dry, deviceId }),
        );
        void queryClient.invalidateQueries({ queryKey: queryKeys.planRuns });
        toast.show({
          tone: "info",
          title: dry ? "已提交 dry-run" : "已提交执行",
          message: `运行 #${res.run_id} 已受理。受理不代表成功，进度见当前运行。`,
          duration: 8000,
        });
        onStarted(res.run_id);
      }}
    >
      <Segmented<Mode>
        aria-label="执行方式"
        className="mt-4"
        block
        options={MODE_OPTIONS}
        value={mode}
        onChange={setMode}
      />
      <div className="confirm-field">
        <label htmlFor={`${ids}-dev`}>关联设备（可选）</label>
        <Select
          id={`${ids}-dev`}
          options={deviceOptions}
          value={deviceId === null ? "" : String(deviceId)}
          onChange={(v) => setDeviceId(v ? Number(v) : null)}
          style={{ width: "100%" }}
        />
        <div className="t-caption faint mt-2">
          关联处于停电周期的设备后，市电恢复确认会取消运行，且同一停电周期只真实执行一次；不关联时市电恢复不影响此运行。
        </div>
      </div>
    </ConfirmNameDialog>
  );
}
