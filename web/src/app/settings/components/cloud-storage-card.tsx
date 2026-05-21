"use client";

import { useState, useEffect, useCallback } from "react";
import {
  Cloud,
  Cookie,
  Plus,
  Trash2,
  RefreshCw,
  CheckCircle2,
  XCircle,
  HelpCircle,
  LoaderCircle,
  Save,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Field, FieldLabel } from "@/components/ui/field";
import { cn } from "@/lib/utils";
import {
  fetchCloudCookies,
  saveCloudCookie,
  deleteCloudCookie,
  checkCloudCookies,
  fetchCloudStorageStatus,
  testCloudUpload,
  type A4Cookie,
} from "@/lib/api";

import { useSettingsStore } from "../store";
import {
  SettingsCard,
  SettingsNotice,
  settingsListItemClassName,
  settingsInputClassName,
} from "./settings-ui";

function maskCookie(value: string) {
  if (!value) return "";
  if (value.length <= 12) return value.slice(0, 4) + "..." + value.slice(-4);
  return value.slice(0, 8) + "..." + value.slice(-4);
}

function formatRelativeTime(value?: string | null) {
  if (!value) return "";
  try {
    const date = new Date(value);
    if (isNaN(date.getTime())) return value;
    const now = Date.now();
    const diffMs = now - date.getTime();
    if (diffMs < 0) return "刚刚";
    const diffSec = Math.floor(diffMs / 1000);
    if (diffSec < 60) return "刚刚";
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}分钟前`;
    const diffHour = Math.floor(diffMin / 60);
    if (diffHour < 24) return `${diffHour}小时前`;
    const diffDay = Math.floor(diffHour / 24);
    if (diffDay < 30) return `${diffDay}天前`;
    return date.toLocaleDateString("zh-CN");
  } catch {
    return value;
  }
}

export function CloudStorageCard() {
  // ── Zustand store: cloud storage settings ──────────────────────────
  const config = useSettingsStore((state) => state.config);
  const isLoadingConfig = useSettingsStore((state) => state.isLoadingConfig);
  const isSavingConfig = useSettingsStore((state) => state.isSavingConfig);
  const saveConfig = useSettingsStore((state) => state.saveConfig);
  const setCloudStorageEnabled = useSettingsStore(
    (state) => state.setCloudStorageEnabled,
  );
  const setCloudStorageUploader = useSettingsStore(
    (state) => state.setCloudStorageUploader,
  );

  // ── Local state: A4 cookies ────────────────────────────────────────
  const [cookies, setCookies] = useState<A4Cookie[]>([]);
  const [isLoadingCookies, setIsLoadingCookies] = useState(true);
  const [isChecking, setIsChecking] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  // Test upload
  const [isTestingUpload, setIsTestingUpload] = useState(false);
  const [testUploadResult, setTestUploadResult] = useState<{
    ok: boolean;
    uploader: string;
    cloud_url: string;
    verify_ok: boolean;
  } | null>(null);

  // Add cookie dialog
  const [dialogOpen, setDialogOpen] = useState(false);
  const [cookieName, setCookieName] = useState("");
  const [cookieValue, setCookieValue] = useState("");
  const [isSavingCookie, setIsSavingCookie] = useState(false);

  // ── Data fetching ──────────────────────────────────────────────────
  const loadCookies = useCallback(async () => {
    setIsLoadingCookies(true);
    try {
      const data = await fetchCloudCookies();
      setCookies(data.cookies ?? []);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载 Cookie 列表失败");
    } finally {
      setIsLoadingCookies(false);
    }
  }, []);

  const loadStatus = useCallback(async () => {
    try {
      const status = await fetchCloudStorageStatus();
      if (status.a4_cookies_total !== undefined) {
        // status data is informative; cookies list drives the UI
      }
    } catch {
      // status fetch is best-effort
    }
  }, []);

  useEffect(() => {
    void loadCookies();
    void loadStatus();
  }, [loadCookies, loadStatus]);

  // ── Cookie CRUD ────────────────────────────────────────────────────
  const handleAddCookie = async () => {
    const name = cookieName.trim();
    const cookie = cookieValue.trim();
    if (!name) {
      toast.error("请输入 Cookie 名称");
      return;
    }
    if (!cookie) {
      toast.error("请输入 Cookie 值");
      return;
    }
    setIsSavingCookie(true);
    try {
      await saveCloudCookie({ name, cookie });
      toast.success("Cookie 已保存");
      setDialogOpen(false);
      setCookieName("");
      setCookieValue("");
      await loadCookies();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存 Cookie 失败");
    } finally {
      setIsSavingCookie(false);
    }
  };

  const handleDeleteCookie = async (id: string) => {
    setDeletingId(id);
    try {
      await deleteCloudCookie(id);
      toast.success("Cookie 已删除");
      await loadCookies();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除 Cookie 失败");
    } finally {
      setDeletingId(null);
    }
  };

  const handleCheckCookies = async () => {
    setIsChecking(true);
    try {
      const data = await checkCloudCookies();
      setCookies(data.cookies ?? []);
      const alive = data.cookies?.filter((c) => c.alive === true).length ?? 0;
      const total = data.cookies?.length ?? 0;
      toast.success(`检测完成：${alive}/${total} 个存活`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "检测 Cookie 失败");
    } finally {
      setIsChecking(false);
    }
  };

  const handleTestUpload = async () => {
    setIsTestingUpload(true);
    setTestUploadResult(null);
    try {
      const result = await testCloudUpload();
      setTestUploadResult(result);
      if (result.ok && result.verify_ok) {
        toast.success("测试上传成功，上传器：" + result.uploader);
      } else if (result.ok) {
        toast.warning("上传成功但解密验证失败，上传器：" + result.uploader);
      } else {
        toast.error("测试上传失败");
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "测试上传失败");
    } finally {
      setIsTestingUpload(false);
    }
  };

  // ── Render helpers ─────────────────────────────────────────────────
  function aliveBadge(alive: boolean | null) {
    if (alive === true) {
      return (
        <Badge variant="success" className="gap-1 rounded-md">
          <CheckCircle2 className="size-3" />
          alive
        </Badge>
      );
    }
    if (alive === false) {
      return (
        <Badge variant="danger" className="gap-1 rounded-md">
          <XCircle className="size-3" />
          dead
        </Badge>
      );
    }
    return (
      <Badge variant="secondary" className="gap-1 rounded-md">
        <HelpCircle className="size-3" />
        unchecked
      </Badge>
    );
  }

  // ── Loading state ──────────────────────────────────────────────────
  if (isLoadingConfig) {
    return (
      <SettingsCard
        icon={Cloud}
        title="云存储设置"
        description="管理云端存储和 A4 Cookie。"
        tone="violet"
      >
        <div className="flex items-center justify-center py-10">
          <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
        </div>
      </SettingsCard>
    );
  }

  return (
    <SettingsCard
      icon={Cloud}
      title="云存储设置"
      description="管理云端存储和 A4 Cookie。"
      tone="violet"
      action={
        <Button
          size="lg"
          onClick={() => void saveConfig()}
          disabled={isSavingConfig}
        >
          {isSavingConfig ? (
            <LoaderCircle data-icon="inline-start" className="animate-spin" />
          ) : (
            <Save data-icon="inline-start" />
          )}
          保存
        </Button>
      }
    >
      <div className="flex flex-col gap-6">
        {/* ── Section A: Cloud Storage Toggle & Preferences ─────────── */}
        <section className="flex flex-col gap-3">
          <div className="flex items-center gap-1.5">
            <h3 className="text-sm leading-6 font-semibold text-foreground">
              云存储开关
            </h3>
          </div>

          <label className="flex min-h-10 min-w-0 items-center gap-2.5 rounded-[12px] border border-border/70 bg-background/75 px-3 py-2 text-sm font-medium text-foreground">
            <Checkbox
              checked={Boolean(config?.cloud_storage_enabled)}
              onCheckedChange={(value) =>
                setCloudStorageEnabled(Boolean(value))
              }
            />
            <span className="min-w-0 leading-5">启用云存储</span>
          </label>

          <Field className="min-w-0 gap-1.5">
            <FieldLabel
              htmlFor="cloud-storage-uploader"
              className="leading-6"
            >
              上传器偏好
            </FieldLabel>
            <select
              id="cloud-storage-uploader"
              value={
                typeof config?.cloud_storage_uploader === "string"
                  ? config.cloud_storage_uploader
                  : "auto"
              }
              onChange={(event) =>
                setCloudStorageUploader(event.target.value)
              }
              className={cn(
                settingsInputClassName,
                "h-11 w-full rounded-[13px] border border-border bg-background px-3 text-sm text-foreground",
              )}
            >
              <option value="auto">Auto</option>
              <option value="a4">A4</option>
              <option value="a1">A1</option>
            </select>
          </Field>
        </section>

        {/* ── Section B: A4 Cookie Management ───────────────────────── */}
        <section className="flex flex-col gap-3">
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-1.5">
              <h3 className="text-sm leading-6 font-semibold text-foreground">
                A4 Cookie 管理
              </h3>
            </div>
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void handleCheckCookies()}
                disabled={isChecking || cookies.length === 0}
              >
                {isChecking ? (
                  <LoaderCircle
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                ) : (
                  <RefreshCw data-icon="inline-start" />
                )}
                检测存活
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void handleTestUpload()}
                disabled={isTestingUpload}
              >
                {isTestingUpload ? (
                  <LoaderCircle
                    data-icon="inline-start"
                    className="animate-spin"
                  />
                ) : (
                  <RefreshCw data-icon="inline-start" />
                )}
                测试上传
              </Button>
              <Button size="sm" onClick={() => setDialogOpen(true)}>
                <Plus data-icon="inline-start" />
                添加
              </Button>
            </div>
          </div>

          {isLoadingCookies ? (
            <div className="flex items-center justify-center py-10">
              <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
            </div>
          ) : cookies.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-3 rounded-[20px] border border-[#f2f3f5] bg-muted/55 px-6 py-10 text-center">
              <Cookie className="size-8 text-muted-foreground/45" />
              <div className="flex flex-col gap-1">
                <p className="text-sm font-medium text-foreground">
                  暂无 A4 Cookie
                </p>
                <p className="text-sm text-muted-foreground">
                  点击「添加」保存你的 A4 Cookie 信息。
                </p>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              {cookies.map((cookie) => {
                const isBusy = deletingId === cookie.id;

                return (
                  <div
                    key={cookie.id}
                    className={cn(
                      settingsListItemClassName,
                      "flex flex-col gap-2",
                    )}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="truncate text-sm font-medium text-foreground">
                          {cookie.name}
                        </span>
                        {aliveBadge(cookie.alive)}
                      </div>
                      <span className="shrink-0 text-xs text-muted-foreground">
                        {cookie.last_checked
                          ? formatRelativeTime(cookie.last_checked)
                          : ""}
                      </span>
                    </div>
                    <div className="flex items-center justify-between gap-3">
                      <span className="min-w-0 truncate text-xs text-muted-foreground">
                        cookie: {maskCookie(cookie.cookie)}
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="shrink-0 text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                        onClick={() => void handleDeleteCookie(cookie.id)}
                        disabled={isBusy}
                        title="删除"
                      >
                        {isBusy ? (
                          <LoaderCircle className="animate-spin" />
                        ) : (
                          <Trash2 />
                        )}
                      </Button>
                    </div>
                    {cookie.error ? (
                      <div className="rounded-[13px] border border-rose-200 bg-rose-50 px-3 py-1.5 text-xs text-rose-700">
                        {cookie.error}
                      </div>
                    ) : null}
                  </div>
                );
              })}
            </div>
          )}

          {/* ── Test Upload Result ────────────────────────────────── */}
          {testUploadResult && (
            <div
              className={cn(
                "rounded-[20px] border px-5 py-4",
                testUploadResult.ok && testUploadResult.verify_ok
                  ? "border-emerald-200 bg-emerald-50"
                  : "border-amber-200 bg-amber-50",
              )}
            >
              <div className="flex flex-col gap-2 text-sm">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-foreground">
                    {testUploadResult.ok && testUploadResult.verify_ok
                      ? "测试上传成功"
                      : testUploadResult.ok
                        ? "上传成功但验证失败"
                        : "测试上传失败"}
                  </span>
                  {testUploadResult.ok && testUploadResult.verify_ok ? (
                    <CheckCircle2 className="size-4 text-emerald-600" />
                  ) : testUploadResult.ok ? (
                    <HelpCircle className="size-4 text-amber-600" />
                  ) : null}
                </div>
                <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                  <span>上传器: {testUploadResult.uploader}</span>
                  <span className="break-all">
                    云 URL: {testUploadResult.cloud_url}
                  </span>
                  <span>验证下载+解密: {testUploadResult.verify_ok ? "通过" : "失败"}</span>
                </div>
              </div>
            </div>
          )}

          <SettingsNotice>
            <p className="font-medium text-foreground">使用说明</p>
            <ul className="mt-1 list-inside list-disc">
              <li>A4 Cookie 用于云端存储服务的身份认证。</li>
              <li>点击「检测存活」批量验证所有 Cookie 是否仍有效。</li>
              <li>点击「测试上传」验证云端存储上传和解密是否正常。</li>
              <li>Cookie 值在界面中部分隐藏显示以保护隐私。</li>
              <li>删除 Cookie 会立即生效且不可恢复。</li>
            </ul>
          </SettingsNotice>
        </section>
      </div>

      {/* ── Add Cookie Dialog ────────────────────────────────────────── */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>添加 A4 Cookie</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-4">
            <Field className="gap-1.5">
              <FieldLabel htmlFor="add-cookie-name" className="leading-6">
                名称
              </FieldLabel>
              <Input
                id="add-cookie-name"
                value={cookieName}
                onChange={(event) => setCookieName(event.target.value)}
                placeholder="例如：账号A"
                className={settingsInputClassName}
              />
            </Field>
            <Field className="gap-1.5">
              <FieldLabel htmlFor="add-cookie-value" className="leading-6">
                Cookie 值
              </FieldLabel>
              <Input
                id="add-cookie-value"
                value={cookieValue}
                onChange={(event) => setCookieValue(event.target.value)}
                placeholder="粘贴完整 Cookie 字符串"
                className={settingsInputClassName}
              />
            </Field>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setDialogOpen(false)}
              disabled={isSavingCookie}
            >
              取消
            </Button>
            <Button
              onClick={() => void handleAddCookie()}
              disabled={isSavingCookie}
            >
              {isSavingCookie ? (
                <LoaderCircle
                  data-icon="inline-start"
                  className="animate-spin"
                />
              ) : (
                <Cookie data-icon="inline-start" />
              )}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </SettingsCard>
  );
}
