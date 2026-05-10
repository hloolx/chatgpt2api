"use client";

import { LoaderCircle, Search, UploadCloud } from "lucide-react";
import { useMemo } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Account } from "@/lib/api";

import { PAGE_SIZE_OPTIONS, useSettingsStore } from "../store";
import { settingsDialogInputClassName } from "./settings-ui";

function accountLabel(account: Account) {
  return (
    String(account.email || "").trim() ||
    String(account.user_id || "").trim() ||
    String(account.token_preview || "").trim() ||
    account.id
  );
}

function accountStatusVariant(status: Account["status"]) {
  switch (status) {
    case "正常":
      return "success";
    case "限流":
      return "warning";
    case "异常":
    case "禁用":
      return "danger";
    default:
      return "secondary";
  }
}

export function ExportBrowserDialog() {
  const exportBrowserOpen = useSettingsStore((state) => state.exportBrowserOpen);
  const exportBrowserPool = useSettingsStore((state) => state.exportBrowserPool);
  const exportAccounts = useSettingsStore((state) => state.exportAccounts);
  const selectedAccountIds = useSettingsStore((state) => state.selectedAccountIds);
  const exportQuery = useSettingsStore((state) => state.exportQuery);
  const exportPage = useSettingsStore((state) => state.exportPage);
  const exportPageSize = useSettingsStore((state) => state.exportPageSize);
  const isStartingExport = useSettingsStore((state) => state.isStartingExport);
  const setExportBrowserOpen = useSettingsStore((state) => state.setExportBrowserOpen);
  const toggleExportAccount = useSettingsStore((state) => state.toggleExportAccount);
  const replaceSelectedAccountIds = useSettingsStore((state) => state.replaceSelectedAccountIds);
  const setExportQuery = useSettingsStore((state) => state.setExportQuery);
  const setExportPage = useSettingsStore((state) => state.setExportPage);
  const setExportPageSize = useSettingsStore((state) => state.setExportPageSize);
  const startExport = useSettingsStore((state) => state.startExport);

  const filteredAccounts = useMemo(() => {
    const query = exportQuery.trim().toLowerCase();
    if (!query) {
      return exportAccounts;
    }
    return exportAccounts.filter((account) => {
      return [
        account.email,
        account.user_id,
        account.token_preview,
        account.type,
        account.status,
        account.id,
      ].some((value) => String(value || "").toLowerCase().includes(query));
    });
  }, [exportAccounts, exportQuery]);

  const currentPageSize = Number(exportPageSize);
  const pageCount = Math.max(1, Math.ceil(filteredAccounts.length / currentPageSize));
  const safePage = Math.min(exportPage, pageCount);
  const pagedAccounts = filteredAccounts.slice(
    (safePage - 1) * currentPageSize,
    safePage * currentPageSize,
  );
  const selectedSet = useMemo(() => new Set(selectedAccountIds), [selectedAccountIds]);
  const selectedAccounts = useMemo(
    () => exportAccounts.filter((account) => selectedSet.has(account.id)),
    [exportAccounts, selectedSet],
  );
  const selectedReadyCount = selectedAccounts.filter((account) => account.cpaExportReady).length;
  const selectedIncompleteCount = Math.max(0, selectedAccounts.length - selectedReadyCount);
  const allFilteredSelected =
    filteredAccounts.length > 0 && filteredAccounts.every((account) => selectedSet.has(account.id));

  const toggleSelectAllFiltered = (checked: boolean) => {
    if (checked) {
      replaceSelectedAccountIds([
        ...selectedAccountIds,
        ...filteredAccounts.map((account) => account.id),
      ]);
      return;
    }
    const filteredSet = new Set(filteredAccounts.map((account) => account.id));
    replaceSelectedAccountIds(selectedAccountIds.filter((id) => !filteredSet.has(id)));
  };

  return (
    <Dialog open={exportBrowserOpen} onOpenChange={setExportBrowserOpen}>
      <DialogContent
        showCloseButton={false}
        className="max-h-[90vh] max-w-5xl rounded-2xl p-6"
      >
        <DialogHeader className="gap-2">
          <DialogTitle>选择要回传的账号</DialogTitle>
          <DialogDescription className="text-sm leading-6">
            {exportBrowserPool
              ? `回传到 ${exportBrowserPool.name || exportBrowserPool.base_url}`
              : "本地号池账号列表"}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="relative min-w-[260px]">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={exportQuery}
              onChange={(event) => setExportQuery(event.target.value)}
              placeholder="搜索 email、ID、token 或状态"
              className={`${settingsDialogInputClassName} pl-10`}
            />
          </div>
          <div className="flex items-center gap-2">
            <Select
              value={exportPageSize}
              onValueChange={(value) =>
                setExportPageSize(value as (typeof PAGE_SIZE_OPTIONS)[number])
              }
            >
              <SelectTrigger className="h-11 w-[120px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {PAGE_SIZE_OPTIONS.map((item) => (
                    <SelectItem key={item} value={item}>
                      {item} / 页
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="lg"
              onClick={() => toggleSelectAllFiltered(!allFilteredSelected)}
            >
              {allFilteredSelected ? "取消全选" : "全选筛选结果"}
            </Button>
          </div>
        </div>

        <div className="rounded-[16px] border border-border/80">
          <div className="flex items-center justify-between border-b border-[#f2f3f5] px-4 py-3 text-sm text-muted-foreground">
            <div className="flex items-center gap-3">
              <Checkbox
                checked={allFilteredSelected}
                onCheckedChange={(checked) => toggleSelectAllFiltered(Boolean(checked))}
              />
              <span>筛选结果 {filteredAccounts.length} 个</span>
            </div>
            <span>
              已选 {selectedAccountIds.length} 个，完整 OAuth {selectedReadyCount} 个
            </span>
          </div>
          <div className="max-h-[420px] overflow-auto">
            {pagedAccounts.length === 0 ? (
              <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
                没有匹配的本地账号
              </div>
            ) : (
              <div className="divide-y divide-[#f2f3f5]">
                {pagedAccounts.map((account) => (
                  <label
                    key={account.id}
                    className="flex cursor-pointer items-center gap-3 px-4 py-3 hover:bg-muted/60"
                  >
                    <Checkbox
                      checked={selectedSet.has(account.id)}
                      onCheckedChange={(checked) =>
                        toggleExportAccount(account.id, Boolean(checked))
                      }
                    />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium text-foreground">
                        {accountLabel(account)}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        {account.token_preview || account.id}
                      </div>
                    </div>
                    <div className="flex shrink-0 flex-wrap justify-end gap-2">
                      <Badge variant="outline" className="rounded-md">
                        {account.type}
                      </Badge>
                      <Badge
                        variant={accountStatusVariant(account.status)}
                        className="rounded-md"
                      >
                        {account.status}
                      </Badge>
                      <Badge
                        variant={account.cpaExportReady ? "success" : "warning"}
                        className="rounded-md"
                      >
                        {account.cpaExportReady ? "完整 OAuth" : "缺少刷新信息"}
                      </Badge>
                    </div>
                  </label>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="flex flex-col gap-3 text-sm text-muted-foreground lg:flex-row lg:items-center lg:justify-between">
          <span>
            第{" "}
            {filteredAccounts.length === 0 ? 0 : (safePage - 1) * currentPageSize + 1}{" "}
            - {Math.min(safePage * currentPageSize, filteredAccounts.length)} 条，共{" "}
            {filteredAccounts.length} 条
          </span>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setExportPage(Math.max(1, safePage - 1))}
              disabled={safePage <= 1}
            >
              上一页
            </Button>
            <span>
              {safePage}/{pageCount}
            </span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setExportPage(Math.min(pageCount, safePage + 1))}
              disabled={safePage >= pageCount}
            >
              下一页
            </Button>
          </div>
        </div>

        {selectedIncompleteCount > 0 ? (
          <div className="rounded-[13px] border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
            已选账号里有 {selectedIncompleteCount} 个缺少 refresh_token 或 id_token。
            仍会尝试回传，但 CPA 侧后续可能无法自动刷新。
          </div>
        ) : null}

        <DialogFooter className="pt-2">
          <Button
            variant="secondary"
            size="lg"
            onClick={() => setExportBrowserOpen(false)}
            disabled={isStartingExport}
          >
            取消
          </Button>
          <Button
            size="lg"
            onClick={() => void startExport()}
            disabled={isStartingExport || selectedAccountIds.length === 0}
          >
            {isStartingExport ? (
              <LoaderCircle data-icon="inline-start" className="animate-spin" />
            ) : (
              <UploadCloud data-icon="inline-start" />
            )}
            回传选中账号
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
