import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { queryKeys } from "../../api/hooks.ts";
import { confirmHostKey, fetchHostKey, type Host } from "../../api/hosts.ts";
import { Banner, Button, Sheet, useToast } from "../../components/index.ts";
import { Icon } from "../../icons/Icon.tsx";
import { useNow } from "../../live/hooks.ts";
import { clockText } from "../../model/format.ts";
import { failureText, HOST_KIND_ICONS, HOST_KIND_LABELS, hostKeyRemaining } from "./model.ts";

interface Candidate {
  fingerprint: string;
  fetchedAt: number;
}

/**
 * 指纹流程（F5-2）：获取 → 人工核对 → 确认 `{confirm:true, fingerprint}`。
 * 获取不发送用户名或凭据；确认只在用户点按后发送，过期后按钮禁用并要求重新获取。
 */
export function FingerprintFlow({ host, onConfirmed }: { host: Host; onConfirmed?: () => void }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const now = useNow(1000);
  const [candidate, setCandidate] = useState<Candidate | null>(null);
  const [busy, setBusy] = useState<"fetch" | "confirm" | null>(null);
  const [error, setError] = useState<string | null>(null);

  const remaining = candidate ? hostKeyRemaining(candidate.fetchedAt, now) : 0;
  const expired = candidate !== null && remaining <= 0;

  const onFetch = async () => {
    setBusy("fetch");
    setError(null);
    try {
      const res = await fetchHostKey(host.id);
      setCandidate({ fingerprint: res.fingerprint, fetchedAt: Date.now() });
    } catch (err) {
      setCandidate(null);
      setError(failureText(err));
    } finally {
      setBusy(null);
    }
  };

  const onConfirm = async () => {
    if (!candidate || expired) return;
    setBusy("confirm");
    setError(null);
    try {
      await confirmHostKey(host.id, candidate.fingerprint);
      await queryClient.invalidateQueries({ queryKey: queryKeys.hosts });
      setCandidate(null);
      toast.show({
        tone: "ok",
        title: "指纹已确认",
        message: `「${host.name}」现在可以认证与执行命令。`,
      });
      onConfirmed?.();
    } catch (err) {
      setError(failureText(err));
    } finally {
      setBusy(null);
    }
  };

  const copy = () => {
    if (!candidate) return;
    navigator.clipboard?.writeText(candidate.fingerprint).then(
      () => toast.show({ tone: "ok", title: "已拷贝指纹", duration: 2000 }),
      () => toast.show({ tone: "warning", title: "无法访问剪贴板，请手动选择拷贝" }),
    );
  };

  return (
    <div className="ha-fp">
      {host.hostkey_fingerprint ? (
        <div className="ha-fp-current">
          <span className="badge badge-green">
            <Icon name="fingerprint" />
            已确认
          </span>
          <code className="mono ha-fp-value">{host.hostkey_fingerprint}</code>
        </div>
      ) : (
        <Banner tone="warning" role="status" title="指纹未确认">
          未确认指纹前，连接测试、测试命令与 SSH 动作都会被拒绝。
        </Banner>
      )}

      {candidate ? (
        <>
          <div className="fp" aria-label="本次获取的指纹">
            {candidate.fingerprint}
          </div>
          <div className="fp-meta">
            <span role="status">
              {expired ? (
                <span className="ink-red">已过期，请重新获取</span>
              ) : (
                <>
                  本次获取的指纹 <b className="tnum">{clockText(remaining)}</b> 内有效
                </>
              )}
            </span>
            <Button variant="plain" size="sm" icon="copy" onClick={copy}>
              拷贝
            </Button>
          </div>
        </>
      ) : null}

      <div className="steps-mini">
        <div>
          <span className={candidate ? "n ok" : "n"}>
            {candidate ? <Icon name="check" /> : "1"}
          </span>
          获取主机公钥。获取过程不发送用户名或密码。
        </div>
        <div>
          <span className="n">2</span>
          通过可信途径（主机控制台或管理界面）查看本机 SSH 指纹，逐字核对。
        </div>
        <div>
          <span className="n">3</span>
          一致后点按「信任此指纹」。之后修改地址或端口会清除已确认的指纹。
        </div>
      </div>

      {error ? (
        <Banner tone="critical" role="alert" className="mt-4">
          {error}
        </Banner>
      ) : null}
      {candidate ? (
        <Banner tone="warning" className="mt-4">
          如果指纹与主机上看到的不一致，不要信任——连接可能被中间人劫持。
        </Banner>
      ) : null}

      <div className="hstack wrap mt-4">
        <Button
          variant={candidate ? "gray" : "tinted"}
          icon="fingerprint"
          disabled={busy !== null}
          aria-busy={busy === "fetch"}
          onClick={() => void onFetch()}
        >
          {busy === "fetch"
            ? "正在获取…"
            : candidate || host.hostkey_fingerprint
              ? "重新获取指纹"
              : "获取指纹"}
        </Button>
        {candidate ? (
          <Button
            variant="primary"
            disabled={busy !== null || expired}
            aria-busy={busy === "confirm"}
            onClick={() => void onConfirm()}
          >
            {busy === "confirm" ? "正在确认…" : "信任此指纹"}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

/** 从主机列表直接打开的指纹浮层。 */
export function FingerprintSheet({ host, onClose }: { host: Host; onClose: () => void }) {
  return (
    <Sheet
      open
      onClose={onClose}
      title="确认主机指纹"
      width="min(520px, calc(100vw - 32px))"
      leading={
        <Button variant="plain" onClick={onClose}>
          完成
        </Button>
      }
    >
      <div className="hstack ha-fp-host">
        <span className="row-icon" style={{ ["--c" as string]: "var(--indigo)" }}>
          <Icon name={HOST_KIND_ICONS[host.kind]} />
        </span>
        <div>
          <div className="t-headline">{host.name}</div>
          <div className="t-footnote muted">
            <span className="mono">
              {host.address}:{host.conn.port}
            </span>{" "}
            · {HOST_KIND_LABELS[host.kind]}
          </div>
        </div>
      </div>
      <FingerprintFlow host={host} onConfirmed={onClose} />
    </Sheet>
  );
}
