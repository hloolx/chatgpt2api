# 生图接口文档

本文档描述当前服务已实现的图片生成、图片编辑和异步创作任务接口。接口分为两组：

- OpenAI 兼容同步接口：`/v1/images/generations`、`/v1/images/edits`。
- Web 端异步任务接口：`/api/creation-tasks/image-generations`、`/api/creation-tasks/image-edits`、查询与取消任务接口。

同步接口适合外部 OpenAI SDK 或简单脚本直接调用；异步任务接口适合 Web 创作台、轮询进度、多图并发、任务取消和结果留存。

## 认证

所有受保护的 AI 接口都需要认证。推荐使用请求头：

```http
Authorization: Bearer <session-or-api-token>
```

也可以由浏览器会话 Cookie 完成认证。普通用户还需要具备对应 API 权限；异步创作任务的权限入口是 `GET /api/creation-tasks` 和 `POST /api/creation-tasks`，子路径按同一资源权限生效。

## 模型与链路

图片任务模型主要使用：

| 模型 | 链路 | 说明 |
| --- | --- | --- |
| `auto` | 官方图片工具 | 默认等价 `gpt-image-2`。 |
| `gpt-image-2` | 官方图片工具 | 走 ChatGPT 官网 `f/conversation` 图片链路。尺寸更接近构图提示，实际像素以上游返回为准。 |
| `codex-gpt-image-2` | Codex 图片链路 | 走 Codex Responses 图片链路，结构化尺寸、格式、JPEG 压缩等参数更直接交给上游工具处理。通常需要 Plus、Team 或 Pro 账号。 |

`/v1/models` 可能返回更多文本模型，但图片生成/图片编辑接口只应使用上述图片任务模型。

## 通用参数

| 字段 | 类型 | 默认值 | 适用接口 | 说明 |
| --- | --- | --- | --- | --- |
| `prompt` | string | 无 | 全部 | 生图或编辑提示词。生成接口必填；编辑接口也建议必填。 |
| `model` | string | `auto` | 全部 | 图片任务模型：`auto`、`gpt-image-2`、`codex-gpt-image-2`。 |
| `n` | number | `1` | 全部 | 生成数量。同步接口要求 `1-10`；异步任务会归一化到 `1-10`。 |
| `size` | string | 空 | 全部 | 支持 `auto`、比例值、档位和显式尺寸。详见“尺寸”。 |
| `quality` | string | 空 | 全部 | 可传 `low`、`medium`、`high`。当前前端不强制启用质量控制，部分链路仅作为提示或上游参数。 |
| `response_format` | string | 同步为 `b64_json`，异步为 `url` | 同步、内部任务 payload | 同步接口可用 `b64_json`；异步任务固定面向 URL 结果。 |
| `output_format` | string | `png` | 全部 | 输出保存格式。支持 `png`、`jpeg`、`webp`，`jpg` 会归一化为 `jpeg`。非法值归一化为 `png`。 |
| `output_compression` | number | 空 | 全部 | 仅 `output_format=jpeg` 时生效，范围 `0-100`，超过 `100` 会按 `100` 处理。 |
| `background` | string | 空 | 全部 | 透传给图片工具的背景参数，例如 `transparent`。实际支持取决于上游链路。 |
| `moderation` | string | 空 | 全部 | 透传给图片工具的审核参数。实际支持取决于上游链路。 |
| `style` | string | 空 | 全部 | 透传给图片工具的风格参数。实际支持取决于上游链路。 |
| `partial_images` | number | 空 | 全部 | 正整数时启用部分图片/进度图片相关参数。 |
| `input_image_mask` | string | 空 | 编辑接口 | 图生图遮罩。通常传 data URL 或 base64 内容，实际支持取决于上游链路。 |
| `visibility` | string | `private` | 全部 | 生成图片入库可见性。支持 `private`、`public`。影响图库展示，不影响上游生成语义。 |
| `messages` | array | 空 | 全部 | 当前会被透传/归一化，但不要把它理解为可靠的“图片上下文记忆”。详见“上下文边界”。 |
| `stream` | boolean | `false` | 同步接口 | 为 `true` 时返回 SSE。 |

## 尺寸

`size` 支持以下写法：

| 写法 | 说明 |
| --- | --- |
| `auto` | 不强制尺寸，由上游决定。 |
| `1080p` | 归一化为 `1080x1080`。 |
| `2k` | 归一化为 `2048x2048`。 |
| `4k` | 归一化为 `2880x2880`。 |
| `1:1`、`3:2`、`2:3`、`16:9`、`21:9`、`9:16`、`4:3`、`3:4` | 作为构图比例提示。 |
| `1024x1024`、`1536x2048` | 显式宽高。官方图片工具链路会把它作为构图/目标尺寸提示，实际像素仍以上游返回为准。 |

异步任务还支持 `image_resolution` 元数据字段，取值为 `1080p`、`2k`、`4k`。该字段用于记录分辨率档位和图库元数据，不替代 `size`。

## 同步文生图

### `POST /v1/images/generations`

请求体格式：`application/json`

必填字段：

- `prompt`

示例：

```bash
curl http://localhost:3000/v1/images/generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <session-or-api-token>" \
  -d '{
    "model": "auto",
    "prompt": "一张雨夜东京街头的赛博朋克猫，霓虹灯反射在地面",
    "n": 1,
    "size": "16:9",
    "output_format": "png",
    "response_format": "b64_json"
  }'
```

成功响应：

```json
{
  "created": 1778470000,
  "data": [
    {
      "url": "http://localhost:3000/images/2026/05/11/example.png",
      "b64_json": "<base64-image>",
      "revised_prompt": "一张雨夜东京街头的赛博朋克猫，霓虹灯反射在地面",
      "output_format": "png"
    }
  ]
}
```

说明：

- `response_format=b64_json` 时返回 `b64_json`，同时仍会保存图片并返回 `url`。
- `response_format` 不为 `b64_json` 时，响应项通常只有 `url`、`revised_prompt`、`output_format`。
- 请求会记录生成图片；`visibility` 控制这些图片在图库中的默认可见性。

## 同步图生图

### `POST /v1/images/edits`

请求体格式：`multipart/form-data`

必填字段：

- `image` 或 `image[]`：至少一个图片文件。
- `prompt`：编辑提示词。

示例：

```bash
curl http://localhost:3000/v1/images/edits \
  -H "Authorization: Bearer <session-or-api-token>" \
  -F "model=auto" \
  -F "prompt=把这张图改成赛博朋克夜景风格，保留主体轮廓" \
  -F "n=1" \
  -F "size=1024x1024" \
  -F "output_format=jpeg" \
  -F "output_compression=85" \
  -F "image=@./input.png"
```

多图参考：

```bash
curl http://localhost:3000/v1/images/edits \
  -H "Authorization: Bearer <session-or-api-token>" \
  -F "model=gpt-image-2" \
  -F "prompt=融合两张参考图的产品外观，生成一张干净的广告图" \
  -F "image[]=@./reference-a.png" \
  -F "image[]=@./reference-b.png"
```

`messages` 如果通过表单传入，必须是 JSON 字符串：

```bash
-F 'messages=[{"role":"user","content":"参考上一轮风格继续生成"}]'
```

## 异步文生图任务

### `POST /api/creation-tasks/image-generations`

请求体格式：`application/json`

必填字段：

- `client_task_id`：客户端生成的任务 ID。同一用户重复提交同一个 ID 会返回已有任务，用于幂等。
- `prompt`

示例：

```bash
curl http://localhost:3000/api/creation-tasks/image-generations \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <session-or-api-token>" \
  -d '{
    "client_task_id": "img-task-20260511-001",
    "model": "gpt-image-2",
    "prompt": "一张用于产品发布会的未来城市主视觉",
    "n": 2,
    "size": "21:9",
    "image_resolution": "2k",
    "output_format": "webp",
    "visibility": "private"
  }'
```

提交成功响应示例：

```json
{
  "id": "img-task-20260511-001",
  "status": "queued",
  "mode": "generate",
  "model": "gpt-image-2",
  "size": "21:9",
  "created_at": "2026-05-11 13:44:41",
  "updated_at": "2026-05-11 13:44:41",
  "output_format": "webp",
  "output_statuses": ["queued", "queued"],
  "visibility": "private"
}
```

任务提交后后台异步执行。调用方需要使用查询接口轮询任务状态。

## 异步图生图任务

### `POST /api/creation-tasks/image-edits`

请求体格式：`multipart/form-data`

必填字段：

- `client_task_id`
- `prompt`
- `image` 或 `image[]`

示例：

```bash
curl http://localhost:3000/api/creation-tasks/image-edits \
  -H "Authorization: Bearer <session-or-api-token>" \
  -F "client_task_id=edit-task-20260511-001" \
  -F "model=auto" \
  -F "prompt=保留人物姿态，改成电影海报质感" \
  -F "n=1" \
  -F "size=3:4" \
  -F "image_resolution=1080p" \
  -F "output_format=png" \
  -F "visibility=public" \
  -F "image=@./portrait.png"
```

带遮罩示例：

```bash
curl http://localhost:3000/api/creation-tasks/image-edits \
  -H "Authorization: Bearer <session-or-api-token>" \
  -F "client_task_id=edit-task-mask-001" \
  -F "prompt=只替换背景为雪山，主体不变" \
  -F "image=@./input.png" \
  -F "input_image_mask=data:image/png;base64,<mask-base64>"
```

## 查询任务

### `GET /api/creation-tasks`

查询当前用户的任务列表：

```bash
curl "http://localhost:3000/api/creation-tasks" \
  -H "Authorization: Bearer <session-or-api-token>"
```

按任务 ID 查询：

```bash
curl "http://localhost:3000/api/creation-tasks?ids=img-task-20260511-001,edit-task-20260511-001" \
  -H "Authorization: Bearer <session-or-api-token>"
```

响应示例：

```json
{
  "items": [
    {
      "id": "img-task-20260511-001",
      "status": "success",
      "mode": "generate",
      "model": "gpt-image-2",
      "size": "21:9",
      "created_at": "2026-05-11 13:44:41",
      "updated_at": "2026-05-11 13:45:12",
      "output_format": "webp",
      "output_statuses": ["success", "success"],
      "visibility": "private",
      "data": [
        {
          "url": "http://localhost:3000/images/2026/05/11/example-1.webp",
          "revised_prompt": "一张用于产品发布会的未来城市主视觉",
          "output_format": "webp"
        },
        {
          "url": "http://localhost:3000/images/2026/05/11/example-2.webp",
          "revised_prompt": "一张用于产品发布会的未来城市主视觉",
          "output_format": "webp"
        }
      ]
    }
  ],
  "missing_ids": []
}
```

## 取消任务

### `POST /api/creation-tasks/{id}/cancel`

示例：

```bash
curl http://localhost:3000/api/creation-tasks/img-task-20260511-001/cancel \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <session-or-api-token>" \
  -d '{}'
```

如果任务仍处于 `queued` 或 `running`，服务会标记为 `cancelled` 并尝试取消后台执行。已完成任务重复取消会返回当前任务状态。

## 任务状态

任务级 `status`：

| 状态 | 含义 |
| --- | --- |
| `queued` | 已入队，等待执行。 |
| `running` | 正在执行。 |
| `success` | 已成功完成。 |
| `error` | 执行失败。错误文本在 `error` 字段。 |
| `cancelled` | 已取消。 |

图片输出级 `output_statuses`：

| 状态 | 含义 |
| --- | --- |
| `queued` | 单张输出等待开始。 |
| `running` | 单张输出正在生成。 |
| `success` | 单张输出已产出图片或文本结果。 |
| `error` | 单张输出失败，或生成成功但本地余额/配额扣减失败因此未交付。 |
| `cancelled` | 单张输出随任务终止。 |

`output_statuses` 的长度通常与 `n` 一致，适合 Web 端逐张展示占位和进度。

## 响应字段

任务对象常见字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 客户端提交的 `client_task_id`。 |
| `status` | string | 任务状态。 |
| `mode` | string | `generate`、`edit` 或 `chat`。本文档关注 `generate` 和 `edit`。 |
| `model` | string | 实际记录的模型。 |
| `size` | string | 请求尺寸或比例。 |
| `quality` | string | 请求质量，未传时可能省略。 |
| `output_format` | string | 归一化后的输出格式。 |
| `output_compression` | number | JPEG 压缩率，仅 JPEG 时可能出现。 |
| `background` | string | 背景参数，传入时可能出现。 |
| `moderation` | string | 审核参数，传入时可能出现。 |
| `style` | string | 风格参数，传入时可能出现。 |
| `partial_images` | number | 部分图片参数，传入正整数时可能出现。 |
| `input_image_mask` | string | 编辑遮罩参数，传入时可能出现。 |
| `output_statuses` | string[] | 单张输出状态。 |
| `data` | array | 输出结果数组。成功后出现。 |
| `error` | string | 失败原因。失败或取消时可能出现。 |
| `output_type` | string | 文本型结果时为 `text`。 |
| `visibility` | string | `private` 或 `public`。 |
| `created_at` | string | 本地时间字符串。 |
| `updated_at` | string | 本地时间字符串。 |

图片结果项常见字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `url` | string | 服务保存后的图片 URL。 |
| `b64_json` | string | base64 图片。同步接口且 `response_format=b64_json` 时返回。 |
| `revised_prompt` | string | 上游或服务记录的最终提示词。 |
| `output_format` | string | 输出格式。 |

图片可见性和 JPEG 压缩率当前主要在任务级字段返回。调用方展示图库状态时优先读取任务级 `visibility`；保存后的图片元数据由服务端图库模块维护。

文本结果项：

```json
{
  "output_type": "text",
  "data": [
    {
      "text_response": "你好！我是 ChatGPT。"
    }
  ]
}
```

## 文本型结果说明

图片接口调用上游后，可能得到文本回复而不是图片。例如用户输入“你好，你是什么模型？”时，上游可能按聊天问题回答而不是调用图片工具。

当前处理方式：

- 同步 `/v1/images/generations` 和 `/v1/images/edits`：返回 OpenAI 风格错误，`code` 为 `image_generation_text_response`，HTTP 状态通常为 `400`。
- 异步 `/api/creation-tasks/image-generations` 和 `/api/creation-tasks/image-edits`：任务会被标记为 `success`，同时返回 `output_type=text` 和 `data[].text_response`，避免 Web 端只显示泛化的失败提示。

调用方如果只接受图片，需要在任务成功后检查：

- `task.output_type !== "text"`
- `task.data[]` 中存在 `url` 或 `b64_json`

## 错误格式

认证失败：

```json
{
  "detail": {
    "error": "authorization is invalid"
  }
}
```

普通参数错误：

```json
{
  "detail": {
    "error": "prompt is required"
  }
}
```

OpenAI 风格图片错误：

```json
{
  "error": {
    "message": "Image generation returned a text response instead of image data.",
    "type": "invalid_request_error",
    "param": null,
    "code": "image_generation_text_response"
  }
}
```

图片额度不足：

```json
{
  "error": {
    "message": "no available image quota",
    "type": "insufficient_quota",
    "param": null,
    "code": "insufficient_quota"
  }
}
```

常见错误：

| HTTP 状态 | 场景 | 错误文本或 code |
| --- | --- | --- |
| `400` | JSON 解析失败 | `invalid json body` |
| `400` | 缺少提示词 | `prompt is required` |
| `400` | 异步任务缺少 ID | `client_task_id is required` |
| `400` | `n` 超出范围 | `n must be between 1 and 10` |
| `400` | 图生图缺少图片 | `image file is required` 或 `image is required` |
| `400` | `messages` 表单字段不是 JSON | `invalid messages` |
| `400` | 非法可见性 | `visibility must be private or public` |
| `400` | 上游返回文本而非图片 | `image_generation_text_response` |
| `401` | 未认证或 token 无效 | `authorization is invalid` |
| `403` | 权限不足 | `permission denied` |
| `429` | 图片额度或任务并发限制 | `insufficient_quota` 或任务限制错误文本 |
| `502` | 上游或协议失败 | `upstream_error`、上游返回的错误详情，或无图片输出时的诊断消息 |

## 上下文边界

当前图片生成接口默认是无状态的：

- 每次 `/v1/images/generations` 请求只应依赖本次请求体。
- 每个 `/api/creation-tasks/image-generations` 任务只应依赖本次任务 payload。
- `messages` 字段会被接收和透传，但当前不保证它等价于 ChatGPT Web 端“对话作画记忆”。
- `visibility`、任务历史、图库记录只用于本地管理，不会自动变成下一次官方图片链路的上下文。

如果未来需要 Web 端对话作画上下文，应使用显式的上下文拼装策略，并按用户、API key、会话隔离。后续扩展设计见 [图片对话上下文设计](image-conversation-context-design.md)。

## 推荐调用流程

Web 端推荐使用异步任务接口：

1. 前端生成唯一 `client_task_id`。
2. 调用 `/api/creation-tasks/image-generations` 或 `/api/creation-tasks/image-edits` 提交任务。
3. 使用 `GET /api/creation-tasks?ids=<client_task_id>` 轮询。
4. 当 `status=success` 且 `output_type` 不是 `text` 时展示 `data[].url`。
5. 当 `status=success` 且 `output_type=text` 时展示 `data[].text_response` 或提示用户改用明确的绘图提示词。
6. 当 `status=error` 或 `cancelled` 时展示 `error`。

外部兼容客户端推荐使用同步接口：

1. 调用 `/v1/images/generations` 或 `/v1/images/edits`。
2. 按 OpenAI 图片响应读取 `data[]`。
3. 对 `error.code=image_generation_text_response` 做单独提示，说明当前提示词没有触发图片输出。
