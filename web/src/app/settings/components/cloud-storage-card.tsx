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
  const setS3Endpoint = useSettingsStore((state) => state.setS3Endpoint);
  const setS3Region = useSettingsStore((state) => state.setS3Region);
  const setS3AccessKeyID = useSettingsStore((state) => state.setS3AccessKeyID);
  const setS3SecretAccessKey = useSettingsStore(
    (state) => state.setS3SecretAccessKey,
  );
  const setS3Bucket = useSettingsStore((state) => state.setS3Bucket);
  const setS3PublicURL = useSettingsStore((state) => state.setS3PublicURL);
  const setS3PathPrefix = useSettingsStore((state) => state.setS3PathPrefix);
  const setS3ForcePathStyle = useSettingsStore(
    (state) => state.setS3ForcePathStyle,
  );
  const setCloudProxy = useSettingsStore((state) => state.setCloudProxy);
  const setCloudProxyEnabled = useSettingsStore((state) => state.setCloudProxyEnabled);

  // ── Local state: A4 cookies ────────────────────────────────────────
  const [cookies, setCookies] = useState<A4Cookie[]>([]);
  const [isLoadingCookies, setIsLoadingCookies] = useState(true);
  const [isChecking, setIsChecking] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  // Test upload
  const [isTestingUpload, setIsTestingUpload] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [testUploadResult, setTestUploadResult] = useState<{
    ok: boolean;
    uploader: string;
    cloud_url: string;
    local_url: string;
    local_path: string;
    verify_ok: boolean;
    direct_url?: string;
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
    if (!selectedFile) {
      toast.error("请先选择一张图片");
      return;
    }
    setIsTestingUpload(true);
    setTestUploadResult(null);
    try {
      const result = await testCloudUpload(selectedFile);
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
              <option value="s3">S3</option>
            </select>
          </Field>
        </section>

        {/* ── Section S3: S3 Storage Configuration ────────────────────── */}
        {config?.cloud_storage_uploader === "s3" && (
          <section className="flex flex-col gap-3">
            <div className="flex items-center gap-1.5">
              <h3 className="text-sm leading-6 font-semibold text-foreground">
                S3 存储配置
              </h3>
            </div>

            <Field className="min-w-0 gap-1.5">
              <FieldLabel htmlFor="s3-endpoint" className="leading-6">
                端点 (Endpoint)
              </FieldLabel>
              <Input
                id="s3-endpoint"
                value={String(config?.s3_endpoint ?? "")}
                onChange={(e) => setS3Endpoint(e.target.value)}
                placeholder="https://xxx.r2.cloudflarestorage.com"
                className={settingsInputClassName}
              />
            </Field>

            <div className="grid grid-cols-2 gap-3">
              <Field className="min-w-0 gap-1.5">
                <FieldLabel htmlFor="s3-region" className="leading-6">
                  区域 (Region)
                </FieldLabel>
                <Input
                  id="s3-region"
                  value={String(config?.s3_region ?? "auto")}
                  onChange={(e) => setS3Region(e.target.value)}
                  placeholder="auto"
                  className={settingsInputClassName}
                />
              </Field>
              <Field className="min-w-0 gap-1.5">
                <FieldLabel htmlFor="s3-bucket" className="leading-6">
                  存储桶 (Bucket)
                </FieldLabel>
                <Input
                  id="s3-bucket"
                  value={String(config?.s3_bucket ?? "")}
                  onChange={(e) => setS3Bucket(e.target.value)}
                  placeholder="chatgpt2api-images"
                  className={settingsInputClassName}
                />
              </Field>
            </div>

            <Field className="min-w-0 gap-1.5">
              <FieldLabel htmlFor="s3-access-key" className="leading-6">
                Access Key ID
              </FieldLabel>
              <Input
                id="s3-access-key"
                value={String(config?.s3_access_key_id ?? "")}
                onChange={(e) => setS3AccessKeyID(e.target.value)}
                placeholder="Access Key ID"
                className={settingsInputClassName}
              />
            </Field>

            <Field className="min-w-0 gap-1.5">
              <FieldLabel htmlFor="s3-secret-key" className="leading-6">
                Secret Access Key
              </FieldLabel>
              <Input
                id="s3-secret-key"
                type="password"
                value={String(config?.s3_secret_access_key ?? "")}
                onChange={(e) => setS3SecretAccessKey(e.target.value)}
                placeholder={
                  config?.s3_secret_access_key_configured
                    ? "已配置，留空则保持不变"
                    : "Secret Access Key"
                }
                className={settingsInputClassName}
              />
            </Field>

            <Field className="min-w-0 gap-1.5">
              <FieldLabel htmlFor="s3-public-url" className="leading-6">
                自定义域名 (可选)
              </FieldLabel>
              <Input
                id="s3-public-url"
                value={String(config?.s3_public_url ?? "")}
                onChange={(e) => setS3PublicURL(e.target.value)}
                placeholder="https://images.yourdomain.com"
                className={settingsInputClassName}
              />
            </Field>

            <div className="grid grid-cols-2 gap-3">
              <Field className="min-w-0 gap-1.5">
                <FieldLabel htmlFor="s3-path-prefix" className="leading-6">
                  对象键前缀 (可选)
                </FieldLabel>
                <Input
                  id="s3-path-prefix"
                  value={String(config?.s3_path_prefix ?? "")}
                  onChange={(e) => setS3PathPrefix(e.target.value)}
                  placeholder="images/"
                  className={settingsInputClassName}
                />
              </Field>
              <Field className="min-w-0 gap-1.5">
                <FieldLabel htmlFor="s3-force-path-style" className="leading-6">
                  路径风格 (MinIO)
                </FieldLabel>
                <label className="flex min-h-10 min-w-0 items-center gap-2.5 rounded-[12px] border border-border/70 bg-background/75 px-3 py-2 text-sm font-medium text-foreground">
                  <Checkbox
                    checked={Boolean(config?.s3_force_path_style)}
                    onCheckedChange={(value) =>
                      setS3ForcePathStyle(Boolean(value))
                    }
                  />
                  <span className="min-w-0 leading-5">启用路径风格</span>
                </label>
              </Field>
            </div>

            <Field className="min-w-0 gap-1.5">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="cloud-proxy-enabled"
                  checked={Boolean(config?.cloud_proxy_enabled ?? true)}
                  onCheckedChange={(value) =>
                    setCloudProxyEnabled(Boolean(value))
                  }
                />
                <FieldLabel htmlFor="cloud-proxy-enabled" className="leading-6">
                  启用云存储专用代理
                </FieldLabel>
              </div>
              <p className="text-xs text-muted-foreground">
                关闭后云存储将直接连接，不使用任何代理
              </p>
            </Field>

            {config?.cloud_proxy_enabled !== false && (
              <Field className="min-w-0 gap-1.5">
                <FieldLabel htmlFor="cloud-proxy" className="leading-6">
                  云存储专用代理地址
                </FieldLabel>
                <Input
                  id="cloud-proxy"
                  value={String(config?.cloud_proxy ?? "")}
                  onChange={(e) => setCloudProxy(e.target.value)}
                  placeholder="例如: http://127.0.0.1:7890"
                  className={settingsInputClassName}
                />
              </Field>
            )}

            <SettingsNotice>
              <p className="font-medium text-foreground">S3 兼容服务</p>
              <ul className="mt-1 list-inside list-disc">
                <li>
                  <strong>Cloudflare R2</strong>: 端点格式
                  https://&lt;account_id&gt;.r2.cloudflarestorage.com，区域填
                  auto
                </li>
                <li>
                  <strong>Backblaze B2</strong>: 端点格式
                  https://s3.us-west-002.backblazeb2.com
                </li>
                <li>
                  <strong>MinIO</strong>: 需勾选「路径风格」
                </li>
                <li>配置后点击页面顶部「保存」生效。</li>
              </ul>
            </SettingsNotice>
          </section>
        )}

        {/* ── Section B: A4 Cookie Management ───────────────────────── */}
        {config?.cloud_storage_uploader !== "s3" && config?.cloud_storage_uploader !== "auto" && (
          <section className="flex flex-col gap-3">
            <Field className="min-w-0 gap-1.5">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="cloud-proxy-enabled-nons3"
                  checked={Boolean(config?.cloud_proxy_enabled ?? true)}
                  onCheckedChange={(value) =>
                    setCloudProxyEnabled(Boolean(value))
                  }
                />
                <FieldLabel htmlFor="cloud-proxy-enabled-nons3" className="leading-6">
                  启用云存储专用代理
                </FieldLabel>
              </div>
              <p className="text-xs text-muted-foreground">
                关闭后云存储将直接连接，不使用任何代理
              </p>
            </Field>

            {config?.cloud_proxy_enabled !== false && (
              <Field className="min-w-0 gap-1.5">
                <FieldLabel htmlFor="cloud-proxy-nons3" className="leading-6">
                  云存储专用代理地址
                </FieldLabel>
                <Input
                  id="cloud-proxy-nons3"
                  value={String(config?.cloud_proxy ?? "")}
                  onChange={(e) => setCloudProxy(e.target.value)}
                  placeholder="例如: http://127.0.0.1:7890"
                  className={settingsInputClassName}
                />
              </Field>
            )}
          </section>
        )}

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
              <div className="flex items-center gap-2">
                <Input
                  type="file"
                  accept="image/*"
                  onChange={(e) => {
                    const file = e.target.files?.[0];
                    setSelectedFile(file ?? null);
                  }}
                  className="w-48 text-xs"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void handleTestUpload()}
                  disabled={isTestingUpload || !selectedFile}
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
              </div>
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
                    云 URL (加密): {testUploadResult.cloud_url}
                  </span>
                  {testUploadResult.local_url && (
                    <div className="mt-2 rounded-md bg-emerald-100 p-2">
                      <span className="font-semibold text-emerald-800">
                        解密后的真实图片地址:
                      </span>
                      <a
                        href={testUploadResult.local_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="mt-1 block break-all text-emerald-700 underline"
                      >
                        {window.location.origin}{testUploadResult.local_url}
                      </a>
                      <span className="mt-1 block text-emerald-600">
                        此地址可直接访问，服务器会自动解密返回原始图片
                      </span>
                    </div>
                  )}
                  {testUploadResult.local_path && (
                    <span className="break-all">
                      本地文件路径: {testUploadResult.local_path}
                    </span>
                  )}
                  {testUploadResult.direct_url && (
                    <div className="mt-2 rounded-md bg-blue-100 p-2">
                      <span className="font-semibold text-blue-800">
                        直链 URL (无需解密):
                      </span>
                      <a
                        href={testUploadResult.direct_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="mt-1 block break-all text-blue-700 underline"
                      >
                        {testUploadResult.direct_url}
                      </a>
                    </div>
                  )}
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
              <li>选择图片后点击「测试上传」验证云端存储上传和解密是否正常。</li>
              <li>测试上传会返回本地访问地址（如 /images/2026/05/22/xxx.png）。</li>
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
