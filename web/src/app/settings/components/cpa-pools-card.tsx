"use client";

import {
  Import,
  LoaderCircle,
  Pencil,
  Plus,
  ServerCog,
  Trash2,
  UploadCloud,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

import { useSettingsStore } from "../store";
import {
  SettingsCard,
  SettingsEmptyState,
  SettingsNotice,
  settingsListItemClassName,
} from "./settings-ui";

export function CPAPoolsCard() {
  const pools = useSettingsStore((state) => state.pools);
  const isLoadingPools = useSettingsStore((state) => state.isLoadingPools);
  const deletingId = useSettingsStore((state) => state.deletingId);
  const loadingFilesId = useSettingsStore((state) => state.loadingFilesId);
  const exportingPoolId = useSettingsStore((state) => state.exportingPoolId);
  const isLoadingExportAccounts = useSettingsStore((state) => state.isLoadingExportAccounts);
  const openAddDialog = useSettingsStore((state) => state.openAddDialog);
  const openEditDialog = useSettingsStore((state) => state.openEditDialog);
  const deletePool = useSettingsStore((state) => state.deletePool);
  const browseFiles = useSettingsStore((state) => state.browseFiles);
  const browseExportAccounts = useSettingsStore((state) => state.browseExportAccounts);

  return (
    <SettingsCard
      icon={ServerCog}
      title="CPA 连接管理"
      description="先配置连接，再按需查询远程账号并选择导入到本地号池。"
      tone="slate"
      meta={pools.length > 0 ? <Badge>{pools.length} 个连接</Badge> : null}
      action={
        <Button onClick={openAddDialog}>
          <Plus data-icon="inline-start" />
          添加连接
        </Button>
      }
    >
      <div className="flex flex-col gap-6">
        {isLoadingPools ? (
          <div className="flex items-center justify-center py-10">
            <LoaderCircle className="size-5 animate-spin text-muted-foreground" />
          </div>
        ) : pools.length === 0 ? (
          <SettingsEmptyState
            icon={ServerCog}
            title="暂无 CPA 连接"
            description="点击「添加连接」保存你的 CLIProxyAPI 信息。"
          />
        ) : (
          <div className="flex flex-col gap-3">
            {pools.map((pool) => {
              const isBusy =
                deletingId === pool.id || loadingFilesId === pool.id || exportingPoolId === pool.id;
              const importJob = pool.import_job ?? null;
              const exportJob = pool.export_job ?? null;
              const progress = importJob?.total
                ? Math.round((importJob.completed / importJob.total) * 100)
                : 0;
              const exportProgress = exportJob?.total
                ? Math.round((exportJob.completed / exportJob.total) * 100)
                : 0;

              return (
                <div
                  key={pool.id}
                  className={`flex flex-col gap-3 ${settingsListItemClassName}`}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="text-sm font-medium text-foreground">
                        {pool.name || pool.base_url}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        {pool.base_url}
                      </div>
                    </div>
                    <div className="flex items-center gap-1">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => openEditDialog(pool)}
                        disabled={isBusy}
                        title="编辑"
                      >
                        <Pencil />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="text-rose-600 hover:bg-rose-50 hover:text-rose-700"
                        onClick={() => void deletePool(pool)}
                        disabled={isBusy}
                        title="删除"
                      >
                        {deletingId === pool.id ? (
                          <LoaderCircle className="animate-spin" />
                        ) : (
                          <Trash2 />
                        )}
                      </Button>
                    </div>
                  </div>

                  <div className="flex items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void browseFiles(pool)}
                      disabled={isBusy}
                    >
                      {loadingFilesId === pool.id ? (
                        <LoaderCircle
                          data-icon="inline-start"
                          className="animate-spin"
                        />
                      ) : (
                        <Import data-icon="inline-start" />
                      )}
                      同步
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => void browseExportAccounts(pool)}
                      disabled={isBusy}
                    >
                      {exportingPoolId === pool.id && isLoadingExportAccounts ? (
                        <LoaderCircle
                          data-icon="inline-start"
                          className="animate-spin"
                        />
                      ) : (
                        <UploadCloud data-icon="inline-start" />
                      )}
                      回传
                    </Button>
                  </div>

                  {importJob ? (
                    <div className="flex flex-col gap-2 rounded-[16px] border border-[#f2f3f5] bg-muted/55 px-3 py-3">
                      <div className="text-xs font-medium tracking-[0.16em] text-muted-foreground uppercase">
                        导入任务
                      </div>
                      <div className="rounded-[13px] border border-border/80 bg-background px-3 py-3">
                        <div className="flex items-center justify-between gap-3">
                          <div className="min-w-0">
                            <div className="text-sm font-medium text-foreground">
                              状态 {importJob.status}，已处理{" "}
                              {importJob.completed}/{importJob.total}
                            </div>
                            <div className="truncate text-xs text-muted-foreground">
                              任务 {importJob.job_id.slice(0, 8)} ·{" "}
                              {importJob.created_at}
                            </div>
                          </div>
                          <Badge
                            variant={
                              importJob.status === "completed"
                                ? "success"
                                : importJob.status === "failed"
                                  ? "danger"
                                  : "info"
                            }
                            className="rounded-md"
                          >
                            {progress}%
                          </Badge>
                        </div>
                        <div className="mt-3 h-2 overflow-hidden rounded-full bg-secondary">
                          <div
                            className="h-full rounded-full bg-[#1456f0] transition-all"
                            style={{ width: `${progress}%` }}
                          />
                        </div>
                        <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
                          <span>新增 {importJob.added}</span>
                          <span>跳过 {importJob.skipped}</span>
                          <span>刷新 {importJob.refreshed}</span>
                          <span>失败 {importJob.failed}</span>
                        </div>
                      </div>
                    </div>
                  ) : null}

                  {exportJob ? (
                    <div className="flex flex-col gap-2 rounded-[16px] border border-[#f2f3f5] bg-muted/55 px-3 py-3">
                      <div className="text-xs font-medium tracking-[0.16em] text-muted-foreground uppercase">
                        回传任务
                      </div>
                      <div className="rounded-[13px] border border-border/80 bg-background px-3 py-3">
                        <div className="flex items-center justify-between gap-3">
                          <div className="min-w-0">
                            <div className="text-sm font-medium text-foreground">
                              状态 {exportJob.status}，已处理{" "}
                              {exportJob.completed}/{exportJob.total}
                            </div>
                            <div className="truncate text-xs text-muted-foreground">
                              任务 {exportJob.job_id.slice(0, 8)} ·{" "}
                              {exportJob.created_at}
                            </div>
                          </div>
                          <Badge
                            variant={
                              exportJob.status === "completed"
                                ? "success"
                                : exportJob.status === "failed"
                                  ? "danger"
                                  : "info"
                            }
                            className="rounded-md"
                          >
                            {exportProgress}%
                          </Badge>
                        </div>
                        <div className="mt-3 h-2 overflow-hidden rounded-full bg-secondary">
                          <div
                            className="h-full rounded-full bg-[#1456f0] transition-all"
                            style={{ width: `${exportProgress}%` }}
                          />
                        </div>
                        <div className="mt-2 flex flex-wrap gap-2 text-xs text-muted-foreground">
                          <span>已上传 {exportJob.exported}</span>
                          <span>缺少 OAuth 元数据 {exportJob.missing_oauth}</span>
                          <span>失败 {exportJob.failed}</span>
                        </div>
                      </div>
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}

        <SettingsNotice>
          <p className="font-medium text-foreground">使用说明</p>
          <ul className="mt-1 list-inside list-disc">
            <li>页面进入后先读取系统里已配置的 CPA 连接。</li>
            <li>
              点击某个连接的「同步」后，会先读取远程账号列表并展示给前端选择。
            </li>
            <li>确认选择后，后端后台下载完整 CPA JSON 并导入本地号池。</li>
            <li>点击「回传」会先展示本地账号，勾选后再按 Codex OAuth JSON 格式上传到 CPA。</li>
          </ul>
        </SettingsNotice>
      </div>
    </SettingsCard>
  );
}
