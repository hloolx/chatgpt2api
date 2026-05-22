"use client";

import { Globe2, LoaderCircle } from "lucide-react";

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

export type PublishImageTarget = {
  conversationId: string;
  turnId: string;
  imageIndex: number;
};

export type PublishRecipeOptions = {
  sharePromptParameters: boolean;
  shareReferenceImages: boolean;
};

interface ImagePublishDialogProps {
  publishImageTarget: PublishImageTarget;
  publishRecipeOptions: PublishRecipeOptions;
  setPublishRecipeOptions: React.Dispatch<React.SetStateAction<PublishRecipeOptions>>;
  visibilityMutatingImageKey: string;
  onConfirm: () => void;
  onClose: () => void;
}

export function ImagePublishDialog({
  publishImageTarget,
  publishRecipeOptions,
  setPublishRecipeOptions,
  visibilityMutatingImageKey,
  onConfirm,
  onClose,
}: ImagePublishDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => (!open && !visibilityMutatingImageKey ? onClose() : null)}>
      <DialogContent showCloseButton={false} className="rounded-2xl p-6">
        <DialogHeader className="gap-2">
          <DialogTitle>公开图片</DialogTitle>
          <DialogDescription className="text-sm leading-6">
            将这张图片加入公开图库。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 py-1">
          <label className="flex items-start gap-3 rounded-xl border border-stone-200 bg-white px-3 py-3 text-sm">
            <Checkbox
              className="mt-0.5"
              checked={publishRecipeOptions.sharePromptParameters}
              onCheckedChange={(checked) =>
                setPublishRecipeOptions({
                  sharePromptParameters: checked === true,
                  shareReferenceImages: checked === true ? publishRecipeOptions.shareReferenceImages : false,
                })
              }
            />
            <span className="min-w-0">
              <span className="block font-medium text-stone-900">公开原始提示词和生成参数</span>
              <span className="mt-0.5 block text-xs leading-5 text-stone-500">公开图库会展示可复用的 prompt、模型、尺寸和输出设置。</span>
            </span>
          </label>
          <label className="flex items-start gap-3 rounded-xl border border-stone-200 bg-white px-3 py-3 text-sm">
            <Checkbox
              className="mt-0.5"
              checked={publishRecipeOptions.shareReferenceImages}
              disabled={!publishRecipeOptions.sharePromptParameters}
              onCheckedChange={(checked) =>
                setPublishRecipeOptions((current) => ({
                  ...current,
                  shareReferenceImages: checked === true,
                }))
              }
            />
            <span className="min-w-0">
              <span className="block font-medium text-stone-900">公开原始参考图用于同款生成</span>
              <span className="mt-0.5 block text-xs leading-5 text-stone-500">其他用户复用时可以读取这些参考图；不勾选时会改用公开成品图。</span>
            </span>
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={visibilityMutatingImageKey !== ""}>
            取消
          </Button>
          <Button onClick={onConfirm} disabled={visibilityMutatingImageKey !== ""}>
            {visibilityMutatingImageKey ? <LoaderCircle className="size-4 animate-spin" /> : <Globe2 className="size-4" />}
            公开
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
