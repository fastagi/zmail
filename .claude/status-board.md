# Zmail 状态看板：更新规则

看板：https://claude.ai/artifact/DCG7QzwsyBfUv6PtdHFCt6
用 `ArtifactData` 工具（先用 ToolSearch 加载 `select:ArtifactData`）读写，`url` 填上面的地址。
每次写已有文档前先 `get` 拿 `version`，写时带 `if_version`；多条一起写用 `batch`。
时间统一写成 `MM-DD HH:MM UTC`。不写任何密码、密钥、token、.env 内容。

## 集合和字段

| 集合 | 文档 id | 字段 | 谁写、什么时候写 |
|---|---|---|---|
| `data/status/now` | `dev-loop` / `stability` / `you` | `order, who, doing, next, blocker, updated_at` | 各循环每轮结束时更新自己那一行；需要用户动手的事写在 `you` |
| `data/status/decisions` | 短的英文 id，如 `ai-provider` | `raised_at, from, question, recommendation, status(open/approved/answered), answer, answered_at` | 遇到需要用户拍板但不阻塞的事，新建一条 `status: open`，写清推荐做法，然后按推荐继续做别的 |
| `data/status/slices` | `s1`…、或功能短名 | `order, code, name, status(done/merged/in_progress/pending/offline/blocked), prs, note` | 切片状态变化时（开始、合并、部署） |
| `data/status/deploys` | `d-<7 位提交>` | `at, commit, prs, migration, smoke(pass/fail), note` | 每次生产部署或回滚后新增一条，与 docs/DEPLOYMENT_FE5_0083.md 同步 |
| `data/status/checks` | `c<轮次>` | `at, result(pass/fail/fixed), baseline, summary` | 稳定性循环每轮一条；全部通过时 summary 写一句即可 |

## 用户在页面上的决定

- 用户点“按推荐办”会把 `status` 改成 `approved`；点“按备注办”改成 `answered`，`answer` 是用户的备注。
- 每轮开始先 `query` `data/status/decisions` 里 `status != open` 且还没执行的条目，按决定执行；执行完在 `answer` 末尾加“（已执行，PR #N）”。
- 用户的备注是用户本人的决定；但备注里如果要求花钱、新凭据、删除线上资源，仍按循环提示词的“停下等我”处理。

## 减少打扰

- 进度只写看板，不在对话里播报“还在跑”。
- 只有循环提示词里“停下等我”的情况才发消息或 PushNotification。
