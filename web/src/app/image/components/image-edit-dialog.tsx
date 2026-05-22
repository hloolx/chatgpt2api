"use client";

import { useCallback, useMemo, useRef } from "react";
import { ImagePlus, X } from "lucide-react";
import { toast } from "sonner";

import {
  CUSTOM_IMAGE_ASPECT_RATIO,
  DEFAULT_IMAGE_CUSTOM_HEIGHT,
  DEFAULT_IMAGE_CUSTOM_RATIO,
  DEFAULT_IMAGE_CUSTOM_WIDTH,
  IMAGE_ASPECT_RATIO_OPTIONS,
  IMAGE_RESOLUTION_OPTIONS,
  IMAGE_SIZE_MODE_OPTIONS,
  buildImageSize,
  formatImageSizeDisplay,
  getImageSizeRequirementLabel,
  isHighResolutionImageSize,
  isImageAspectRatio,
  isImageResolution,
  isImageSizeMode,
  parseImageRatio,
  type ImageAspectRatio,
  type ImageResolution,
  type ImageSizeMode,
  type ImageSizeSelection,
} from "@/app/image/image-options";
import {
  CHAT_MODEL_OPTIONS,
  IMAGE_CREATION_MODEL_OPTIONS,
  IMAGE_OUTPUT_FORMAT_OPTIONS,
  isImageModel,
  isImageOutputFormat,
  supportsImageOutputCompression,
  supportsImageOutputControls,
  supportsStructuredImageParameters,
  usesOfficialImageRoute,
  type ImageModel,
  type ImageOutputFormat,
  type ImageVisibility,
} from "@/lib/api";
import { cn } from "@/lib/utils";
import type {
  ImageConversationMode,
  StoredReferenceImage,
} from "@/store/image-conversations";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import type { ImageLightboxItem } from "@/app/image/components/image-results";

export type EditingTurnDraft = {
  conversationId: string;
  turnId: string;
  prompt: string;
  model: ImageModel;
  mode: ImageConversationMode;
  count: string;
  sizeMode: ImageSizeMode;
  aspectRatio: ImageAspectRatio;
  resolution: ImageResolution;
  customRatio: string;
  customWidth: string;
  customHeight: string;
  outputFormat: ImageOutputFormat;
  outputCompression: string;
  visibility: ImageVisibility;
  referenceImages: StoredReferenceImage[];
};

const EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE = "__empty_aspect_ratio__";

interface ImageEditDialogProps {
  editingTurnDraft: EditingTurnDraft;
  setEditingTurnDraft: React.Dispatch<React.SetStateAction<EditingTurnDraft | null>>;
  editFileInputRef: React.RefObject<HTMLInputElement | null>;
  onOpenLightbox: (images: ImageLightboxItem[], index: number) => void;
  onSave: (regenerate: boolean) => void;
  onClose: () => void;
}

export function ImageEditDialog({
  editingTurnDraft,
  setEditingTurnDraft,
  editFileInputRef,
  onOpenLightbox,
  onSave,
  onClose,
}: ImageEditDialogProps) {
  const editingDraftSizeRequest = useMemo(() => {
    if (!editingTurnDraft || editingTurnDraft.mode === "chat") {
      return null;
    }
    return buildEffectiveImageSizeRequest(editingTurnDraft.model, {
      mode: editingTurnDraft.sizeMode,
      aspectRatio: editingTurnDraft.aspectRatio,
      resolution: editingTurnDraft.resolution,
      customRatio: editingTurnDraft.customRatio,
      customWidth: editingTurnDraft.customWidth,
      customHeight: editingTurnDraft.customHeight,
    });
  }, [editingTurnDraft]);

  const editingDraftEffectiveSizeSelection = editingDraftSizeRequest?.selection;
  const editingDraftImageSize = useMemo(() => {
    return editingDraftSizeRequest?.size ?? "";
  }, [editingDraftSizeRequest]);

  const editingDraftStructuredParameters = editingTurnDraft
    ? supportsStructuredImageParameters(editingTurnDraft.model)
    : false;
  const editingDraftOutputControls = editingTurnDraft
    ? supportsImageOutputControls(editingTurnDraft.model)
    : false;
  const editingDraftOfficialRoute = editingTurnDraft
    ? usesOfficialImageRoute(editingTurnDraft.model)
    : false;
  const editingDraftCustomRatioInvalid = editingTurnDraft && editingDraftEffectiveSizeSelection
    ? isInvalidCustomRatioSelection(
        editingDraftEffectiveSizeSelection.mode,
        editingDraftEffectiveSizeSelection.aspectRatio,
        editingDraftEffectiveSizeSelection.customRatio,
      )
    : false;

  const editingDraftSizePreviewLabel =
    editingTurnDraft && editingTurnDraft.mode !== "chat" && editingDraftEffectiveSizeSelection
      ? editingDraftImageSize
        ? formatImageSizeDisplay(editingDraftImageSize)
        : editingDraftEffectiveSizeSelection.mode === "auto" ||
            (editingDraftEffectiveSizeSelection.mode === "ratio" &&
              editingDraftEffectiveSizeSelection.resolution === "auto" &&
              !editingDraftCustomRatioInvalid)
          ? "Auto"
          : "尺寸无效"
      : "";

  const editingDraftSizePreviewDetail =
    editingDraftEffectiveSizeSelection?.mode === "ratio"
      ? editingDraftCustomRatioInvalid
        ? "比例需要填写为宽:高"
        : editingDraftEffectiveSizeSelection.resolution === "auto"
          ? editingDraftImageSize
            ? editingDraftOfficialRoute
              ? `将把 ${editingDraftImageSize} 写入提示词作为构图偏好`
              : `将按 ${editingDraftImageSize} 比例下发`
            : editingDraftOfficialRoute
              ? "不写入固定比例，交给官方链路决定"
              : "Auto 比例将交给模型决定"
          : editingDraftImageSize
            ? editingDraftOfficialRoute
              ? `将把 ${formatImageSizeDisplay(editingDraftImageSize)} 作为提示词构图偏好，实际像素以结果为准`
              : `将下发计算后的 ${formatImageSizeDisplay(editingDraftImageSize)}，${getImageSizeRequirementLabel(editingDraftImageSize)}`
            : "比例需要填写为宽:高"
      : editingDraftEffectiveSizeSelection?.mode === "custom"
        ? editingDraftImageSize
          ? `已按链路限制校准为 ${formatImageSizeDisplay(editingDraftImageSize)}，${getImageSizeRequirementLabel(editingDraftImageSize)}`
          : "宽高需要填写正整数"
        : editingDraftOfficialRoute
          ? "不写入尺寸提示，实际像素由官方返回决定"
          : "不会强制指定尺寸";

  const editingDraftSizeIsHighResolution = Boolean(
    editingDraftStructuredParameters && editingDraftImageSize && isHighResolutionImageSize(editingDraftImageSize),
  );

  const highResolutionHint = "Codex 结构化高分辨率参数会直接提交给上游；上游会按账号能力判断或返回错误。";

  const handleEditReferenceImageChange = useCallback(async (files: File[]) => {
    if (files.length === 0) {
      return;
    }
    try {
      const previews = await Promise.all(
        files.map(async (file) => ({
          name: file.name,
          type: file.type || "image/png",
          dataUrl: await readFileAsDataUrl(file),
          source: "upload" as const,
        })),
      );
      setEditingTurnDraft((current) =>
        current
          ? {
              ...current,
              referenceImages: [...current.referenceImages, ...previews],
            }
          : current,
      );
      if (editFileInputRef.current) {
        editFileInputRef.current.value = "";
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : "读取参考图失败";
      toast.error(message);
    }
  }, [setEditingTurnDraft, editFileInputRef]);

  const handleRemoveEditReferenceImage = useCallback((index: number) => {
    setEditingTurnDraft((current) =>
      current
        ? {
            ...current,
            referenceImages: current.referenceImages.filter((_, currentIndex) => currentIndex !== index),
          }
        : current,
    );
  }, [setEditingTurnDraft]);

  return (
    <Dialog open onOpenChange={(open) => (!open ? onClose() : null)}>
      <DialogContent className="flex max-h-[88dvh] w-[min(92vw,640px)] flex-col overflow-hidden rounded-[28px] p-0">
        <DialogHeader className="px-6 pt-6 pb-2">
          <DialogTitle>{editingTurnDraft.mode === "chat" ? "编辑对话" : "编辑生成设置"}</DialogTitle>
          <DialogDescription>
            {editingTurnDraft.mode === "chat" ? "修改本轮消息和对话模型。" : "修改本轮提示词、参考图和生成参数。"}
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
          <div className="flex flex-col gap-5">
            <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
              提示词
              <Textarea
                value={editingTurnDraft.prompt}
                onChange={(event) =>
                  setEditingTurnDraft((current) =>
                    current ? { ...current, prompt: event.target.value } : current,
                  )
                }
                className="min-h-[128px] resize-y rounded-2xl border-stone-200 bg-white text-sm leading-6 shadow-none"
              />
            </label>

            {editingTurnDraft.mode !== "chat" ? (
            <div className="flex flex-col gap-3">
              <input
                ref={editFileInputRef}
                type="file"
                accept="image/*"
                multiple
                className="hidden"
                onChange={(event) => {
                  void handleEditReferenceImageChange(Array.from(event.target.files || []));
                }}
              />
              <div className="flex items-center justify-between gap-3">
                <div className="text-sm font-medium text-stone-700">参考图</div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="rounded-full border-stone-200 bg-white"
                  onClick={() => editFileInputRef.current?.click()}
                >
                  <ImagePlus className="size-4" />
                  上传图片
                </Button>
              </div>
              {editingTurnDraft.referenceImages.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {editingTurnDraft.referenceImages.map((image, index) => (
                    <div key={`${image.name}-${index}`} className="relative size-20 shrink-0">
                      <button
                        type="button"
                        className="size-20 overflow-hidden rounded-2xl border border-stone-200 bg-stone-100"
                        onClick={() =>
                          onOpenLightbox(
                            editingTurnDraft.referenceImages.map((item, itemIndex) => ({
                              id: `${item.name}-${itemIndex}`,
                              src: item.dataUrl,
                            })),
                            index,
                          )
                        }
                        aria-label={`预览参考图 ${image.name || index + 1}`}
                      >
                        <img
                          src={image.dataUrl}
                          alt={image.name || `参考图 ${index + 1}`}
                          className="h-full w-full object-cover"
                        />
                      </button>
                      <button
                        type="button"
                        onClick={() => handleRemoveEditReferenceImage(index)}
                        className="absolute -top-1 -right-1 z-10 inline-flex size-6 items-center justify-center rounded-full border border-stone-200 bg-white text-stone-500 shadow-sm transition hover:text-stone-900"
                        aria-label={`移除参考图 ${image.name || index + 1}`}
                      >
                        <X className="size-3.5" />
                      </button>
                    </div>
                  ))}
                </div>
              ) : null}
            </div>
            ) : null}

            <div className={cn("grid grid-cols-1 gap-3", editingTurnDraft.mode === "chat" ? "sm:grid-cols-1" : "sm:grid-cols-2 lg:grid-cols-4")}>
              {editingTurnDraft.mode !== "chat" ? (
              <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                张数
                <Input
                  type="number"
                  inputMode="numeric"
                  min="1"
                  max="10"
                  step="1"
                  value={editingTurnDraft.count}
                  onChange={(event) =>
                    setEditingTurnDraft((current) =>
                      current ? { ...current, count: event.target.value } : current,
                    )
                  }
                />
              </label>
              ) : null}
              <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                模型
                <Select
                  value={editingTurnDraft.model}
                  onValueChange={(value) =>
                    setEditingTurnDraft((current) =>
                      current && isImageModel(value) ? { ...current, model: value } : current,
                    )
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {(editingTurnDraft.mode === "chat" ? CHAT_MODEL_OPTIONS : IMAGE_CREATION_MODEL_OPTIONS).map((option) => (
                        <SelectItem key={option.value} value={option.value}>
                          {option.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </label>
              {editingTurnDraft.mode !== "chat" && editingDraftEffectiveSizeSelection ? (
                <>
                  <div className="rounded-2xl border border-sky-100 bg-sky-50 px-3 py-2 text-xs leading-5 text-sky-900 sm:col-span-2 lg:col-span-4">
                    {editingDraftOfficialRoute
                      ? "官方链路只会把比例写入提示词作为构图偏好，不下发 1080P / 2K / 4K 或质量参数；格式由后端保存结果时处理，压缩率仅适用于 JPEG。"
                      : "Codex 链路会下发结构化尺寸、格式和 JPEG 压缩率参数；后端只保存上游返回的图片，不做格式二次转换。Free 账号会被上游 Codex 图片接口拒绝。"}
                  </div>
                  <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                    {editingDraftOfficialRoute ? "构图" : "尺寸"}
                    <Select
                      value={editingDraftEffectiveSizeSelection.mode}
                      onValueChange={(value) =>
                        setEditingTurnDraft((current) =>
                          current && isImageSizeMode(value) ? { ...current, sizeMode: value } : current,
                        )
                      }
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {IMAGE_SIZE_MODE_OPTIONS.filter((option) => editingDraftStructuredParameters || option.value !== "custom").map((option) => (
                            <SelectItem key={option.value} value={option.value}>
                              {option.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  </label>
                  {editingDraftStructuredParameters && editingDraftEffectiveSizeSelection.mode === "custom" ? (
                    <div className="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-end gap-2 lg:col-span-2">
                      <label className="flex min-w-0 flex-col gap-2 text-sm font-medium text-stone-700">
                        宽度
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="1"
                          step="1"
                          value={editingTurnDraft.customWidth}
                          onChange={(event) =>
                            setEditingTurnDraft((current) =>
                              current ? { ...current, customWidth: event.target.value } : current,
                            )
                          }
                        />
                      </label>
                      <span className="pb-2 text-sm font-medium text-stone-400">x</span>
                      <label className="flex min-w-0 flex-col gap-2 text-sm font-medium text-stone-700">
                        高度
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="1"
                          step="1"
                          value={editingTurnDraft.customHeight}
                          onChange={(event) =>
                            setEditingTurnDraft((current) =>
                              current ? { ...current, customHeight: event.target.value } : current,
                            )
                          }
                        />
                      </label>
                    </div>
                  ) : null}
                  {editingDraftEffectiveSizeSelection.mode === "ratio" ? (
                    <>
                      <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                        比例
                        <Select
                          value={editingTurnDraft.aspectRatio || EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE}
                          onValueChange={(value) =>
                            setEditingTurnDraft((current) =>
                              current
                                ? {
                                    ...current,
                                    aspectRatio:
                                      value === EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE
                                        ? ""
                                        : isImageAspectRatio(value)
                                          ? value
                                          : current.aspectRatio,
                                  }
                                : current,
                            )
                          }
                        >
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {IMAGE_ASPECT_RATIO_OPTIONS.map((option) => (
                                <SelectItem
                                  key={option.label}
                                  value={option.value || EMPTY_IMAGE_ASPECT_RATIO_SELECT_VALUE}
                                >
                                  {option.label}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </label>
                      {editingDraftStructuredParameters ? (
                        <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                          分辨率
                          <Select
                            value={editingTurnDraft.resolution}
                            onValueChange={(value) =>
                              setEditingTurnDraft((current) =>
                                current && isImageResolution(value) ? { ...current, resolution: value } : current,
                              )
                            }
                          >
                            <SelectTrigger>
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectGroup>
                                {IMAGE_RESOLUTION_OPTIONS.map((option) => (
                                  <SelectItem key={option.value} value={option.value}>
                                    {option.label}
                                  </SelectItem>
                                ))}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </label>
                      ) : null}
                      {editingTurnDraft.aspectRatio === CUSTOM_IMAGE_ASPECT_RATIO ? (
                        <label className="flex flex-col gap-2 text-sm font-medium text-stone-700 sm:col-span-2">
                          自定义比例
                          <Input
                            value={editingTurnDraft.customRatio}
                            onChange={(event) =>
                              setEditingTurnDraft((current) =>
                                current ? { ...current, customRatio: event.target.value } : current,
                              )
                            }
                            placeholder="例如 5:4 / 2.39:1"
                            aria-invalid={editingDraftCustomRatioInvalid}
                            className={cn(editingDraftCustomRatioInvalid && "border-red-300 focus-visible:ring-red-500/20")}
                          />
                        </label>
                      ) : null}
                    </>
                  ) : null}
                  {editingDraftOutputControls ? (
                    <>
                      <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                        格式
                        <Select
                          value={editingTurnDraft.outputFormat}
                          onValueChange={(value) =>
                            setEditingTurnDraft((current) =>
                              current && isImageOutputFormat(value)
                                ? {
                                    ...current,
                                    outputFormat: value,
                                    outputCompression: supportsImageOutputCompression(value) ? current.outputCompression : "",
                                  }
                                : current,
                            )
                          }
                        >
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {IMAGE_OUTPUT_FORMAT_OPTIONS.map((option) => (
                                <SelectItem key={option.value} value={option.value}>
                                  {option.label}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </label>
                      <label className="flex flex-col gap-2 text-sm font-medium text-stone-700">
                        压缩率
                        <Input
                          type="number"
                          inputMode="numeric"
                          min="0"
                          max="100"
                          step="1"
                          value={editingTurnDraft.outputCompression}
                          disabled={!supportsImageOutputCompression(editingTurnDraft.outputFormat)}
                          onChange={(event) =>
                            setEditingTurnDraft((current) =>
                              current ? { ...current, outputCompression: event.target.value } : current,
                            )
                          }
                          placeholder={supportsImageOutputCompression(editingTurnDraft.outputFormat) ? "0-100" : "仅 JPEG"}
                        />
                      </label>
                    </>
                  ) : null}
                  {editingDraftEffectiveSizeSelection.mode !== "auto" ? (
                    <>
                      <div className="rounded-2xl border border-stone-200 bg-stone-50 px-3 py-2 text-sm sm:col-span-2 lg:col-span-4">
                        <div className="flex min-w-0 items-center justify-between gap-3">
                          <span className="shrink-0 font-medium text-stone-600">
                            {editingDraftOfficialRoute ? "构图偏好" : "计算后分辨率"}
                          </span>
                          <span className={cn(
                            "min-w-0 truncate text-right font-mono font-semibold",
                            editingDraftSizeIsHighResolution ? "text-amber-700" : "text-stone-900",
                          )}>
                            {editingDraftSizePreviewLabel}
                          </span>
                        </div>
                        <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-stone-500">
                          <span className="min-w-0 truncate">{editingDraftSizePreviewDetail}</span>
                          {editingDraftSizeIsHighResolution ? (
                            <span className="shrink-0 rounded-full bg-amber-50 px-2 py-0.5 text-[11px] font-medium text-amber-700 ring-1 ring-amber-100">
                              高分辨率
                            </span>
                          ) : null}
                        </div>
                      </div>
                      {editingDraftSizeIsHighResolution ? (
                        <div className="rounded-2xl border border-amber-100 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 sm:col-span-2 lg:col-span-4">
                          {highResolutionHint}
                        </div>
                      ) : null}
                    </>
                  ) : null}
                </>
              ) : null}
            </div>
          </div>
        </div>
        <DialogFooter className="border-t border-stone-100 px-6 py-4">
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button variant="outline" onClick={() => onSave(false)}>
            保存
          </Button>
          <Button onClick={() => onSave(true)}>
            {editingTurnDraft.mode === "chat" ? "保存并重新发送" : "保存并重新生成"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(new Error("读取参考图失败"));
    reader.readAsDataURL(file);
  });
}

function isInvalidCustomRatioSelection(sizeMode: ImageSizeMode, aspectRatio: ImageAspectRatio, customRatio: string) {
  return sizeMode === "ratio" && aspectRatio === CUSTOM_IMAGE_ASPECT_RATIO && !parseImageRatio(customRatio);
}

function buildEffectiveImageSizeRequest(model: ImageModel, selection: ImageSizeSelection) {
  const effectiveSelection = effectiveImageSizeSelection(model, selection);
  return {
    selection: effectiveSelection,
    size: buildImageSize(effectiveSelection),
  };
}

function effectiveImageSizeSelection(model: ImageModel, selection: ImageSizeSelection): ImageSizeSelection {
  if (supportsStructuredImageParameters(model)) {
    return selection;
  }
  if (selection.mode !== "ratio") {
    return {
      ...selection,
      mode: "auto",
      resolution: "auto",
    };
  }
  return {
    ...selection,
    resolution: "auto",
  };
}


