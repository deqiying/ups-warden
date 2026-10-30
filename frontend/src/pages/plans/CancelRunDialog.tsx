import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { isApiError } from "../../api/client.ts";
import { queryKeys } from "../../api/hooks.ts";
import { cancelPlanRun } from "../../api/plans.ts";
import type { PlanRun } from "../../api/types.ts";
import { Button, Dialog, useToast } from "../../components/index.ts";
import { failureText } from "../hosts-actions/model.ts";

export interface CancelRunDialogProps {
  run: Pick<PlanRun, "id" | "plan_name" | "status">;
  open: boolean;
  onClose: () => void;
}

/**
 * 取消计划运行（F6-4，计划页与总览停电主卡共用）。
 * 用户确认后才提交 `{confirm:true}`；202 只表示未开始的步骤已取消，执行中的步骤不被打断，
 * 已发出的命令不可撤回。409 plan_run_finished：运行已结束；503 plan_unavailable：服务关闭中。
 */
export function CancelRunDialog({ run, open, onClose }: CancelRunDialogProps) {
  if (!open) return null;
  return <CancelRunDialogBody run={run} onClose={onClose} />;
}

function CancelRunDialogBody({ run, onClose }: Omit<CancelRunDialogProps, "open">) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const countdown = run.status === "countdown";

  const confirm = async () => {
    setBusy(true);
    setFailure(null);
    try {
      await cancelPlanRun(run.id);
      toast.show({
        tone: "info",
        title: "已提交取消",
        message: countdown
          ? `运行 #${run.id} 的取消已受理，所有步骤都不会开始。`
          : `运行 #${run.id} 的取消已受理：未开始的步骤不再执行，执行中的步骤会继续到结束，已发出的命令无法撤回。`,
        duration: 8000,
      });
      void queryClient.invalidateQueries({ queryKey: queryKeys.planRuns });
      onClose();
    } catch (err) {
      if (isApiError(err) && err.code === "plan_run_finished") {
        // 运行已在其他地方结束（完成、市电恢复或他人取消）：刷新后关闭即可。
        void queryClient.invalidateQueries({ queryKey: queryKeys.planRuns });
        toast.show({ tone: "info", title: "运行已结束", message: "无需取消。", duration: 5000 });
        onClose();
        return;
      }
      setFailure(failureText(err));
      setBusy(false);
    }
  };

  return (
    <Dialog
      open
      onClose={onClose}
      dismissible={!busy}
      icon="slash-circle"
      tone="danger"
      title={`取消「${run.plan_name}」运行 #${run.id}？`}
      message={
        countdown
          ? "倒计时将停止，计划中的所有步骤都不会开始。"
          : "尚未开始的步骤将被取消。正在执行的步骤会继续到结束，已经发出的关机命令无法撤回。"
      }
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            继续运行
          </Button>
          <Button variant="danger" disabled={busy} aria-busy={busy} onClick={() => void confirm()}>
            {busy ? "正在提交…" : "取消运行"}
          </Button>
        </>
      }
    >
      {failure ? (
        <div className="dialog-error t-footnote ink-red" role="alert">
          {failure}
        </div>
      ) : null}
    </Dialog>
  );
}
