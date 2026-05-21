"use client";

import { useState, useCallback, useEffect } from "react";
import { LoaderCircle, Mail, MailOpen, Plus, Trash2, RefreshCw, ChevronDown, ChevronRight } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "sonner";

import {
  hlooLMailGenerate,
  hlooLMailMailboxes,
  hlooLMailDeleteMailbox,
  hlooLMailEmailsNext,
  hlooLMailEmails,
  hlooLMailEmailsRead,
  hlooLMailEmailsClear,
  type HLOOLMailbox,
  type HLOOLEmailMessage,
} from "@/lib/api";
import { useSettingsStore } from "../../settings/store";

export function HloolMailToolbox() {
  const config = useSettingsStore((state) => state.registerConfig);
  const providers = (config?.mail?.providers || []) as Array<Record<string, unknown>>;

  // Find all enabled hlool_mail providers
  const hloolProviders = providers
    .map((p, i) => ({ ...p, _index: i }) as Record<string, unknown> & { _index: number })
    .filter((p) => String(p.type) === "hlool_mail" && String(p.api_key || "").trim() !== "");

  const [selectedIndex, setSelectedIndex] = useState(0);
  const [mailboxes, setMailboxes] = useState<HLOOLMailbox[]>([]);
  const [loadingMailboxes, setLoadingMailboxes] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [prefix, setPrefix] = useState("");
  const [genDomain, setGenDomain] = useState("");
  const [expanded, setExpanded] = useState(true);
  const [viewingEmail, setViewingEmail] = useState<HLOOLEmailMessage | null>(null);
  const [loadingEmail, setLoadingEmail] = useState(false);
  const [emailList, setEmailList] = useState<HLOOLEmailMessage[]>([]);
  const [loadingEmailList, setLoadingEmailList] = useState(false);
  const [selectedMailbox, setSelectedMailbox] = useState<string>("");

  const currentProvider = hloolProviders[selectedIndex] as Record<string, unknown> | undefined;
  const apiKey = String(currentProvider?.api_key || "");
  const apiBase = String(currentProvider?.api_base || "");

  const fetchMailboxes = useCallback(async () => {
    if (!apiKey) return;
    setLoadingMailboxes(true);
    try {
      const res = await hlooLMailMailboxes(apiKey, 1, 50, apiBase || undefined);
      if (res.success && res.data?.items) {
        setMailboxes(res.data.items);
      } else {
        toast.error(res.error || "获取邮箱列表失败");
      }
    } catch {
      toast.error("网络请求失败");
    } finally {
      setLoadingMailboxes(false);
    }
  }, [apiKey, apiBase]);

  useEffect(() => {
    if (apiKey) {
      fetchMailboxes();
    }
  }, [apiKey, fetchMailboxes]);

  const handleGenerate = async () => {
    if (!apiKey) return;
    setGenerating(true);
    try {
      const payload: { prefix?: string; domain?: string } = {};
      if (prefix.trim()) payload.prefix = prefix.trim();
      if (genDomain.trim()) payload.domain = genDomain.trim();
      const res = await hlooLMailGenerate(apiKey, payload, apiBase || undefined);
      if (res.success && res.data?.email) {
        toast.success(`邮箱创建成功: ${res.data.email}`);
        setPrefix("");
        setGenDomain("");
        fetchMailboxes();
      } else {
        toast.error(res.error || "创建邮箱失败");
      }
    } catch {
      toast.error("网络请求失败");
    } finally {
      setGenerating(false);
    }
  };

  const handleDelete = async (id: number, email: string) => {
    if (!apiKey || !confirm(`确认删除邮箱 ${email} 及其所有邮件？`)) return;
    try {
      const res = await hlooLMailDeleteMailbox(apiKey, id, apiBase || undefined);
      if (res.success) {
        toast.success(`已删除 ${email}`);
        fetchMailboxes();
        if (selectedMailbox === email) {
          setSelectedMailbox("");
          setEmailList([]);
          setViewingEmail(null);
        }
      } else {
        toast.error(res.error || "删除失败");
      }
    } catch {
      toast.error("网络请求失败");
    }
  };

  const handleFetchEmails = async (email: string) => {
    if (!apiKey) return;
    setLoadingEmailList(true);
    setSelectedMailbox(email);
    try {
      const res = await hlooLMailEmails(apiKey, email, 1, 20, apiBase || undefined);
      if (res.success && res.data?.items) {
        setEmailList(res.data.items);
      } else {
        setEmailList([]);
      }
    } catch {
      toast.error("获取邮件失败");
    } finally {
      setLoadingEmailList(false);
    }
  };

  const handleReadEmail = async (id: string) => {
    if (!apiKey) return;
    setLoadingEmail(true);
    try {
      const res = await hlooLMailEmailsRead(apiKey, id, apiBase || undefined);
      if (res.success && res.data) {
        setViewingEmail(res.data as HLOOLEmailMessage);
      } else {
        toast.error(res.error || "读取邮件失败");
      }
    } catch {
      toast.error("网络请求失败");
    } finally {
      setLoadingEmail(false);
    }
  };

  const handleCheckNext = async (email: string) => {
    if (!apiKey) return;
    try {
      const res = await hlooLMailEmailsNext(apiKey, email, apiBase || undefined);
      if (res.success && res.data?.has_email && res.data?.message) {
        setViewingEmail(res.data.message);
        toast.success("获取到新邮件");
      } else {
        toast.info("暂无未读邮件");
      }
    } catch {
      toast.error("网络请求失败");
    }
  };

  const handleClearEmails = async (email: string) => {
    if (!apiKey || !confirm(`确认清空 ${email} 的所有邮件？`)) return;
    try {
      const res = await hlooLMailEmailsClear(apiKey, email, apiBase || undefined);
      if (res.success) {
        toast.success(`已清空 ${email} 的邮件`);
        setEmailList([]);
        setViewingEmail(null);
      } else {
        toast.error(res.error || "清空失败");
      }
    } catch {
      toast.error("网络请求失败");
    }
  };

  if (hloolProviders.length === 0) {
    return null;
  }

  return (
    <div className="rounded-[24px] border border-[#f2f3f5] bg-card p-4 shadow-[0_0_15px_rgba(44,30,116,0.16)]">
      <button
        type="button"
        className="flex w-full items-center justify-between gap-3"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="flex items-center gap-3">
          <div className="flex size-9 items-center justify-center rounded-md bg-muted">
            <Mail className="size-5 text-muted-foreground" />
          </div>
          <div className="text-left">
            <h2 className="text-lg font-semibold tracking-tight">HLOOL Mail 工具箱</h2>
            <p className="text-xs text-stone-500">管理邮箱与查看邮件</p>
          </div>
        </div>
        {expanded ? <ChevronDown className="size-5" /> : <ChevronRight className="size-5" />}
      </button>

      {expanded && (
        <div className="mt-4 grid gap-4 xl:grid-cols-2">
          {/* Left: Generate + Mailbox List */}
          <div className="space-y-4">
            {/* Provider selector (if multiple) */}
            {hloolProviders.length > 1 ? (
              <div className="flex items-center gap-2 text-sm">
                <span className="text-stone-500">Provider:</span>
                <select
                  className="rounded-lg border border-stone-200 bg-white px-2 py-1 text-xs"
                  value={selectedIndex}
                  onChange={(e) => setSelectedIndex(Number(e.target.value))}
                >
                  {hloolProviders.map((p, i) => (
                    <option key={i} value={i}>
                      {String((p as Record<string, unknown>).api_base || "https://email.hlool.cc")}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}

            {/* Generate Mailbox */}
            <div className="rounded-xl border border-stone-200 bg-stone-50 p-3">
              <h3 className="mb-2 text-sm font-medium text-stone-700">生成邮箱</h3>
              <div className="flex flex-wrap gap-2">
                <Input
                  value={prefix}
                  onChange={(e) => setPrefix(e.target.value)}
                  placeholder="前缀 (可选)"
                  className="h-9 w-32 rounded-xl border-stone-200 bg-white text-sm"
                />
                <Input
                  value={genDomain}
                  onChange={(e) => setGenDomain(e.target.value)}
                  placeholder="域名 (可选，留空随机)"
                  className="h-9 min-w-40 flex-1 rounded-xl border-stone-200 bg-white text-sm"
                />
                <Button
                  type="button"
                  className="h-9 rounded-xl bg-stone-950 px-4 text-white hover:bg-stone-800"
                  onClick={handleGenerate}
                  disabled={generating}
                >
                  {generating ? <LoaderCircle className="size-4 animate-spin" /> : <Plus className="size-4" />}
                  生成
                </Button>
              </div>
            </div>

            {/* Mailbox List */}
            <div className="rounded-xl border border-stone-200">
              <div className="flex items-center justify-between border-b border-stone-100 px-3 py-2">
                <span className="text-sm font-medium text-stone-700">
                  邮箱列表
                  {mailboxes.length > 0 && (
                    <Badge variant="secondary" className="ml-1.5 rounded-md px-1.5 py-0 text-[10px]">{mailboxes.length}</Badge>
                  )}
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-7 rounded-lg px-2 text-xs"
                  onClick={fetchMailboxes}
                  disabled={loadingMailboxes}
                >
                  <RefreshCw className={`size-3 ${loadingMailboxes ? "animate-spin" : ""}`} />
                </Button>
              </div>
              <div className="max-h-64 overflow-y-auto">
                {loadingMailboxes ? (
                  <div className="flex items-center justify-center py-6">
                    <LoaderCircle className="size-4 animate-spin text-stone-300" />
                  </div>
                ) : mailboxes.length === 0 ? (
                  <p className="px-3 py-4 text-center text-xs text-stone-400">暂无邮箱，点击上方"生成"创建</p>
                ) : (
                  mailboxes.map((mb) => (
                    <div
                      key={mb.id}
                      className={`flex items-center justify-between border-b border-stone-50 px-3 py-2 last:border-b-0 ${
                        selectedMailbox === mb.email ? "bg-stone-50" : ""
                      }`}
                    >
                      <button
                        type="button"
                        className="text-left text-xs font-mono text-stone-700 hover:text-stone-950"
                        onClick={() => handleFetchEmails(mb.email)}
                      >
                        {mb.email}
                      </button>
                      <div className="flex items-center gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-6 rounded-md px-1.5 text-xs text-stone-400 hover:text-stone-600"
                          onClick={() => handleCheckNext(mb.email)}
                        >
                          查新
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-6 rounded-md px-1.5 text-xs text-rose-400 hover:text-rose-600"
                          onClick={() => handleDelete(mb.id, mb.email)}
                        >
                          <Trash2 className="size-3" />
                        </Button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
          </div>

          {/* Right: Email Viewer */}
          <div className="space-y-4">
            {/* Email List for selected mailbox */}
            {selectedMailbox ? (
              <div className="rounded-xl border border-stone-200">
                <div className="flex items-center justify-between border-b border-stone-100 px-3 py-2">
                  <span className="text-sm font-medium text-stone-700 truncate max-w-[200px]">
                    {selectedMailbox}
                  </span>
                  <div className="flex items-center gap-1">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 rounded-lg px-2 text-xs text-rose-500"
                      onClick={() => handleClearEmails(selectedMailbox)}
                    >
                      清空
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 rounded-lg px-2 text-xs"
                      onClick={() => handleFetchEmails(selectedMailbox)}
                      disabled={loadingEmailList}
                    >
                      <RefreshCw className={`size-3 ${loadingEmailList ? "animate-spin" : ""}`} />
                    </Button>
                  </div>
                </div>
                <div className="max-h-48 overflow-y-auto">
                  {loadingEmailList ? (
                    <div className="flex items-center justify-center py-6">
                      <LoaderCircle className="size-4 animate-spin text-stone-300" />
                    </div>
                  ) : emailList.length === 0 ? (
                    <p className="px-3 py-4 text-center text-xs text-stone-400">暂无邮件</p>
                  ) : (
                    emailList.map((msg) => (
                      <button
                        key={msg.id}
                        type="button"
                        className="flex w-full items-start gap-2 border-b border-stone-50 px-3 py-2 text-left last:border-b-0 hover:bg-stone-50"
                        onClick={() => handleReadEmail(msg.id)}
                      >
                        <MailOpen className="mt-0.5 size-3.5 shrink-0 text-stone-300" />
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-xs font-medium text-stone-700">{msg.subject || "(无主题)"}</p>
                          <p className="truncate text-[10px] text-stone-400">{msg.from_address}</p>
                        </div>
                      </button>
                    ))
                  )}
                </div>
              </div>
            ) : (
              <div className="flex items-center justify-center rounded-xl border border-stone-200 py-10">
                <p className="text-xs text-stone-400">点击左侧邮箱查看邮件</p>
              </div>
            )}

            {/* Email Detail */}
            {viewingEmail ? (
              <div className="rounded-xl border border-stone-200 p-3">
                <div className="mb-2 flex items-center justify-between">
                  <h3 className="text-sm font-medium text-stone-700 truncate">{viewingEmail.subject || "(无主题)"}</h3>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-6 rounded-md px-1.5 text-xs"
                    onClick={() => setViewingEmail(null)}
                  >
                    关闭
                  </Button>
                </div>
                <div className="space-y-1 text-xs text-stone-500">
                  <p>发件人: {viewingEmail.from_address}</p>
                  <p>时间: {viewingEmail.created_at}</p>
                </div>
                <div className="mt-2 max-h-64 overflow-y-auto rounded-lg bg-stone-50 p-3">
                  {viewingEmail.text_content ? (
                    <pre className="whitespace-pre-wrap font-sans text-xs text-stone-700">{viewingEmail.text_content}</pre>
                  ) : viewingEmail.html_content ? (
                    <div className="text-xs" dangerouslySetInnerHTML={{ __html: viewingEmail.html_content }} />
                  ) : (
                    <p className="text-xs text-stone-400">无法显示邮件内容</p>
                  )}
                </div>
              </div>
            ) : null}
          </div>
        </div>
      )}
    </div>
  );
}
