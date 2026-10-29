import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useRef, useState, type ChangeEvent } from "react";
import { useSearchParams } from "react-router";
import { isApiError } from "../../api/client.ts";
import { queryKeys, useProfiles } from "../../api/hooks.ts";
import {
  copyProfile,
  createProfile,
  deleteProfile,
  getProfile,
  profileErrorLocation,
  profileExportUrl,
  validateProfile,
} from "../../api/profiles.ts";
import type { ProfileDetail, ProfileSummary, ProfileValidation } from "../../api/types.ts";
import { Badge } from "../../components/Badge.tsx";
import { Banner } from "../../components/Banner.tsx";
import { Button } from "../../components/Button.tsx";
import { DestructiveDialog } from "../../components/DestructiveDialog.tsx";
import { Dialog } from "../../components/Dialog.tsx";
import { Empty } from "../../components/Empty.tsx";
import { Input, Textarea } from "../../components/Input.tsx";
import { Row } from "../../components/Row.tsx";
import { Sheet } from "../../components/Sheet.tsx";
import { Spinner } from "../../components/Spinner.tsx";
import { useToast } from "../../components/toast-context.ts";
import { Icon } from "../../icons/Icon.tsx";
import { Page } from "../../layout/Page.tsx";
import { SplitView } from "../../layout/SplitView.tsx";
import { protocolLabel } from "../../model/device.ts";
import { profileErrorText, lineExcerpt } from "./model.ts";
import "./profiles.css";

/** 声明大小上限（monitoring-api.md「Profile」）。 */
const MAX_CONTENT_BYTES = 256 * 1024;

/**
 * Profile 管理（F3-4，无效果图，Q-D1：按列表-详情简版实现）。
 * 列表、查看声明原文、导入（先校验，错误显示行号与路径）、复制、导出、删除；编辑器与校准视图为 P1。
 */
export function ProfilesPage() {
  const [search, setSearch] = useSearchParams();
  const profiles = useProfiles();
  const [importing, setImporting] = useState(false);
  const selectedKey = search.get("key") ?? profiles.data?.[0]?.key ?? null;
  const select = (key: string) => {
    const p = new URLSearchParams(search);
    p.set("key", key);
    setSearch(p, { replace: true });
  };

  return (
    <Page
      title="Profile"
      subtitle="Profile 把协议原始数据映射为统一字段。内置 Profile 只读，可复制后导出修改再导入。"
      toolbar={
        <Button variant="primary" size="sm" icon="plus" onClick={() => setImporting(true)}>
          导入
        </Button>
      }
    >
      {profiles.isPending ? (
        <div className="card">
          <Empty title="正在加载 Profile…">
            <Spinner />
          </Empty>
        </div>
      ) : profiles.isError ? (
        <Banner
          tone="critical"
          role="alert"
          title="Profile 加载失败"
          actions={
            <Button size="sm" onClick={() => void profiles.refetch()}>
              重试
            </Button>
          }
        >
          {profiles.error.message}
        </Banner>
      ) : (
        <SplitView
          listLabel="Profile 列表"
          stickyList
          list={<ProfileList items={profiles.data} selected={selectedKey} onSelect={select} />}
          detail={
            selectedKey ? (
              <ProfileDetailCard key={selectedKey} profileKey={selectedKey} onSelect={select} />
            ) : (
              <div className="card">
                <Empty icon="doc" tone="info" title="还没有 Profile" />
              </div>
            )
          }
        />
      )}
      {importing ? (
        <ImportSheet
          onClose={() => setImporting(false)}
          onImported={(key) => {
            setImporting(false);
            select(key);
          }}
        />
      ) : null}
    </Page>
  );
}

function ProfileList({
  items,
  selected,
  onSelect,
}: {
  items: readonly ProfileSummary[];
  selected: string | null;
  onSelect: (key: string) => void;
}) {
  const builtin = items.filter((p) => p.builtin);
  const custom = items.filter((p) => !p.builtin);
  const group = (title: string, list: readonly ProfileSummary[]) => (
    <>
      <div className="list-header">{title}</div>
      {list.length === 0 ? (
        <div className="list">
          <div className="row">
            <span className="row-main muted t-footnote">暂无，可导入或复制内置 Profile</span>
          </div>
        </div>
      ) : (
        <div className="list">
          {list.map((p) => (
            <Row
              key={p.key}
              title={<span className="mono">{p.key}</span>}
              sub={`${p.name} · ${protocolLabel({ protocol: p.protocol, params: null })} · v${p.version}`}
              selected={p.key === selected}
              onActivate={() => onSelect(p.key)}
              chevron
            />
          ))}
        </div>
      )}
    </>
  );
  return (
    <>
      {group("内置（只读）", builtin)}
      <div className="mt-4">{group("自定义", custom)}</div>
    </>
  );
}

function ProfileDetailCard({
  profileKey,
  onSelect,
}: {
  profileKey: string;
  onSelect: (key: string) => void;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const detail = useQuery<ProfileDetail>({
    queryKey: [...queryKeys.profiles, profileKey],
    queryFn: ({ signal }) => getProfile(profileKey, signal),
  });
  const [copying, setCopying] = useState(false);
  const [deleting, setDeleting] = useState(false);

  if (detail.isPending) {
    return (
      <div className="card">
        <Empty title="正在加载声明…">
          <Spinner />
        </Empty>
      </div>
    );
  }
  if (detail.isError) {
    const notFound = isApiError(detail.error) && detail.error.status === 404;
    return notFound ? (
      <div className="card">
        <Empty icon="question" tone="info" title="找不到这个 Profile">
          它可能已被删除。
        </Empty>
      </div>
    ) : (
      <Banner
        tone="critical"
        role="alert"
        title="声明加载失败"
        actions={
          <Button size="sm" onClick={() => void detail.refetch()}>
            重试
          </Button>
        }
      >
        {detail.error.message}
      </Banner>
    );
  }

  const p = detail.data;
  return (
    <>
      <section className="card" aria-label={`Profile ${p.key}`}>
        <div className="card-head">
          <div style={{ minWidth: 0 }}>
            <h3 className="card-title mono">{p.key}</h3>
            <div className="card-sub">{p.name}</div>
            <div className="hstack wrap gap-1 mt-2">
              <Badge tone="blue">{protocolLabel({ protocol: p.protocol, params: null })}</Badge>
              <Badge>v{p.version}</Badge>
              {p.builtin ? (
                <Badge tone="outline" icon="lock">
                  内置只读
                </Badge>
              ) : (
                <Badge tone="indigo">自定义</Badge>
              )}
            </div>
          </div>
          <div className="hstack wrap">
            <Button size="sm" icon="copy" onClick={() => setCopying(true)}>
              复制
            </Button>
            <a
              className="btn btn-gray btn-sm"
              href={profileExportUrl(p.key)}
              download={`${p.key}.yaml`}
            >
              <Icon name="download" />
              导出
            </a>
            {!p.builtin ? (
              <Button
                size="sm"
                variant="danger-tinted"
                icon="trash"
                onClick={() => setDeleting(true)}
              >
                删除
              </Button>
            ) : null}
          </div>
        </div>
        <div className="card-body">
          <div className="list-header" style={{ paddingLeft: 0 }}>
            声明原文
          </div>
          <pre className="code-block profile-source" tabIndex={0} aria-label="声明原文">
            {p.content}
          </pre>
          <p className="list-caption" style={{ paddingLeft: 0 }}>
            {p.builtin
              ? "内置 Profile 不可修改或删除。需要调整映射时，先复制为自定义 Profile，导出修改后再导入为新 key。"
              : "在线编辑器与校准视图将在后续版本提供；目前可导出修改后以新 key 重新导入。"}
          </p>
        </div>
      </section>
      {copying ? (
        <CopyDialog
          source={p}
          onClose={() => setCopying(false)}
          onCopied={(created) => {
            setCopying(false);
            void queryClient.invalidateQueries({ queryKey: queryKeys.profiles });
            toast.show({ tone: "ok", title: `已复制为「${created.key}」` });
            onSelect(created.key);
          }}
        />
      ) : null}
      <DestructiveDialog
        open={deleting}
        onClose={() => setDeleting(false)}
        title={`删除 Profile「${p.key}」？`}
        message="删除后不可撤销。仍被数据源引用的 Profile 无法删除。"
        conflictMessage="仍有数据源使用这个 Profile，无法删除。请先把这些数据源改用其他 Profile。"
        onConfirm={async () => {
          await deleteProfile(p.key);
          queryClient.removeQueries({ queryKey: [...queryKeys.profiles, p.key] });
          void queryClient.invalidateQueries({ queryKey: queryKeys.profiles });
          toast.show({ tone: "ok", title: `已删除「${p.key}」` });
        }}
      />
    </>
  );
}

function CopyDialog({
  source,
  onClose,
  onCopied,
}: {
  source: ProfileSummary;
  onClose: () => void;
  onCopied: (created: ProfileDetail) => void;
}) {
  const ids = useId();
  const [key, setKey] = useState(`${source.key}-custom`);
  const [name, setName] = useState(`${source.name}（副本）`);
  const copy = useMutation<ProfileDetail, Error>({
    mutationFn: () => copyProfile(source.key, { key: key.trim(), name: name.trim() }),
    onSuccess: onCopied,
  });
  const invalid = !key.trim() || !name.trim();
  return (
    <Dialog
      open
      wide
      onClose={onClose}
      dismissible={!copy.isPending}
      title={`复制「${source.key}」`}
      message="复制为新的自定义 Profile，版本从 1 开始；原 Profile 不受影响。"
      actions={
        <>
          <Button onClick={onClose} disabled={copy.isPending}>
            取消
          </Button>
          <Button
            variant="primary"
            disabled={invalid || copy.isPending}
            aria-busy={copy.isPending}
            onClick={() => copy.mutate()}
          >
            {copy.isPending ? "正在复制…" : "复制"}
          </Button>
        </>
      }
    >
      <div className="vstack gap-3" style={{ textAlign: "left" }}>
        <label className="t-footnote muted" htmlFor={`${ids}-key`}>
          新 key
        </label>
        <Input id={`${ids}-key`} mono value={key} onChange={(e) => setKey(e.target.value)} />
        <label className="t-footnote muted" htmlFor={`${ids}-name`}>
          名称
        </label>
        <Input id={`${ids}-name`} value={name} onChange={(e) => setName(e.target.value)} />
        {copy.isError ? (
          <div className="t-footnote ink-red" role="alert">
            {isApiError(copy.error) && copy.error.code === "resource_conflict"
              ? "该 key 已存在，请换一个。"
              : profileErrorText(copy.error)}
          </div>
        ) : null}
      </div>
    </Dialog>
  );
}

type CheckState =
  | { kind: "idle" }
  | { kind: "valid"; profile: ProfileSummary }
  | { kind: "invalid"; message: string; line: number | null };

function ImportSheet({
  onClose,
  onImported,
}: {
  onClose: () => void;
  onImported: (key: string) => void;
}) {
  const ids = useId();
  const queryClient = useQueryClient();
  const toast = useToast();
  const [content, setContent] = useState("");
  const [check, setCheck] = useState<CheckState>({ kind: "idle" });
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const tooLarge = new TextEncoder().encode(content).length > MAX_CONTENT_BYTES;

  const toCheck = (error: unknown): CheckState => {
    const line = isApiError(error) ? profileErrorLocation(error.details).line : null;
    return { kind: "invalid", message: profileErrorText(error), line };
  };

  const validate = useMutation<ProfileValidation, Error, string>({
    mutationFn: (text) => validateProfile(text),
    onSuccess: (res) => setCheck({ kind: "valid", profile: res.profile }),
    onError: (error) => setCheck(toCheck(error)),
  });
  const create = useMutation<ProfileDetail, Error, string>({
    mutationFn: (text) => createProfile(text),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.profiles });
      toast.show({ tone: "ok", title: `已导入「${created.key}」` });
      onImported(created.key);
    },
    onError: (error) => setCheck(toCheck(error)),
  });
  const busy = validate.isPending || create.isPending;

  const onFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    if (file.size > MAX_CONTENT_BYTES) {
      setCheck({ kind: "invalid", message: "文件超过 256 KiB 上限", line: null });
      return;
    }
    setContent(await file.text());
    setCheck({ kind: "idle" });
  };

  /** 把光标移到出错行，便于在文本框内修正。 */
  const focusLine = (line: number) => {
    const el = textareaRef.current;
    if (!el) return;
    const lines = content.split("\n");
    const start = lines.slice(0, line - 1).reduce((n, l) => n + l.length + 1, 0);
    const end = start + (lines[line - 1]?.length ?? 0);
    el.focus();
    el.setSelectionRange(start, end);
  };

  return (
    <Sheet
      open
      onClose={onClose}
      dismissible={!busy}
      title="导入 Profile"
      width="min(760px, calc(100vw - 32px))"
      trailing={
        <>
          <Button
            variant="plain"
            disabled={!content.trim() || tooLarge || busy}
            onClick={() => validate.mutate(content)}
          >
            {validate.isPending ? "正在校验…" : "校验"}
          </Button>
          <Button
            variant="primary"
            size="sm"
            disabled={!content.trim() || tooLarge || busy}
            aria-busy={create.isPending}
            onClick={() => create.mutate(content)}
          >
            {create.isPending ? "正在导入…" : "导入"}
          </Button>
        </>
      }
    >
      <div className="hstack wrap gap-3" style={{ marginBottom: 12 }}>
        <label className="btn btn-gray btn-sm" htmlFor={`${ids}-file`}>
          <Icon name="doc" />
          选择文件…
        </label>
        <input
          id={`${ids}-file`}
          type="file"
          accept=".yaml,.yml,.json,application/json,application/yaml,text/yaml"
          className="sr-only"
          onChange={(e) => void onFile(e)}
        />
        <span className="t-footnote muted">也可以直接粘贴 YAML / JSON 声明，最大 256 KiB。</span>
      </div>
      <Textarea
        ref={textareaRef}
        id={`${ids}-content`}
        aria-label="Profile 声明"
        mono
        rows={18}
        spellCheck={false}
        className="profile-editor"
        value={content}
        invalid={check.kind === "invalid" || tooLarge}
        onChange={(e) => {
          setContent(e.target.value);
          if (check.kind !== "idle") setCheck({ kind: "idle" });
        }}
      />
      {tooLarge ? (
        <Banner tone="critical" role="alert" className="mt-3">
          声明超过 256 KiB 上限。
        </Banner>
      ) : null}
      {check.kind === "valid" ? (
        <Banner tone="info" icon="check-circle" role="status" className="mt-3" title="校验通过">
          <span className="mono">{check.profile.key}</span> · {check.profile.name} ·{" "}
          {protocolLabel({ protocol: check.profile.protocol, params: null })} · v
          {check.profile.version}
        </Banner>
      ) : null}
      {check.kind === "invalid" ? (
        <Banner
          tone="critical"
          role="alert"
          className="mt-3"
          title={check.line ? `第 ${check.line} 行有误` : "校验未通过"}
          actions={
            check.line ? (
              <Button size="sm" onClick={() => focusLine(check.line!)}>
                定位到第 {check.line} 行
              </Button>
            ) : null
          }
        >
          {check.message}
          {check.line ? (
            <code className="profile-excerpt">{lineExcerpt(content, check.line)}</code>
          ) : null}
        </Banner>
      ) : null}
      <p className="list-caption" style={{ paddingLeft: 0 }}>
        导入只新建自定义 Profile；内置 key 与已存在的 key 会被拒绝。Profile
        只能声明只读请求，校验会拒绝任何写入或控制指令。
      </p>
    </Sheet>
  );
}
