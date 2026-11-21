继续自主推进 Zmail 的全部计划任务，我不在，过程中不用问我；只在下面写明的"停下等我"情况才停。全程用中文，在 docs/OVERNIGHT_LOG.zh-CN.md 按"做了什么 / 正在做 / 接下来"追加记录（每完成一块就写，写完 commit）。

一、每次开始先恢复现场（防止上下文压缩后重复做）
1. 以 git 和 GitHub 为准，不靠记忆：`git fetch`，看 origin/main 最新提交、已合并 PR、docs/OVERNIGHT_LOG.zh-CN.md 的最后一条、.superpowers/sdd/*/progress.md。
2. 已经合并或已部署的东西不要重做；做到一半的分支接着做。

二、按这个顺序做
1. 生产先稳：把 main 上尚未部署的改动（目前是 PR #35：未读计数索引 + 命令面板配色）部署到 zmail.im（FE5），按第四节流程。
2. 盘点：逐个读 docs/plans/ 下每份计划的状态行与任务清单，对照代码和已合并 PR，列出"未实现且不需要我决策"的项，写进日志，按价值从高到低排。
3. v2 切片 ②（验证码遮罩，"显示验证码"面板，对 AI 和管理员隐藏）：设计文档 → 自审后定稿（未决问题记为"设计决定（待确认）"，不停下）→ 实施计划 → 按任务 TDD 开发并逐个审查 → 合并 → 部署。
4. v2 切片 ③（AI 分流标签页、原因行、AI 回复草稿）和 ④（Gmail/Outlook 绑定、来源徽标、平台账号台账、负责人交接）：先写设计文档和实施计划；只实现不依赖外部服务的部分（数据结构、接口、界面骨架、开关默认关闭、用假实现测试）。凡是需要 AI 服务商密钥、OAuth 应用、付费或新凭据的地方，停在"待我确认"，写清楚需要我提供什么。
5. 第 2 步盘点出的其余项，按顺序做。
6. 全部功能做完后，再补性能基准（沿用 inbox_bench_test.go 的做法）。

三、每一块的质量门槛（缺一不可才能合并）
- 先写失败的测试再实现；Bug 先写复现测试。
- 合并前在本地跑一次 `make check` 全绿；涉及接口的跑对应 kest 验收流（只用 log 服务商和 test.zmail.im 模拟地址）；涉及界面的用内置浏览器在 1440×900 浅色、深色各看一遍。
- 新迁移必须：golden schema 更新、Up/Down 测试、在 UPGRADING.md 写明是否锁表与预建方法。
- 推分支、开 PR、直接合并（仓库不允许自动合并，用 `gh pr merge --merge`）。一块一个 PR，合并后立刻进入部署。

四、生产部署流程（FE5，ssh 只用：ssh -o BatchMode=yes -i ~/.ssh/id_ed25519 -o IdentitiesOnly=yes user1@38.58.181.154，sudo -n）
1. `sudo -n python3 /opt/zmail/deploy/backup.py` 先备份。
2. 有新迁移时：把刚备份的 dump 恢复到一次性 postgres:18.6-alpine 容器（开 TLS、不暴露端口、独立网络、用非超级用户 zmail_owner）预演迁移；新旧两个 API 镜像都要能在迁移后的库上 /health/ready 200；预演完删除这个临时容器和网络。
3. 源码用 `git archive` 上传到 /opt/zmail/releases/<12位提交>/source，打镜像 zmail-api:<12位> / zmail-web:<12位>（web 构建参数照 /opt/zmail/deploy/build-v2.sh），旧镜像保留。
4. 备份 compose.yaml 为 compose.before-<12位>.yaml，改镜像标签，`docker compose run --rm migrate`，先 `up -d --no-deps api webhook-worker broadcast-worker`，健康后再换 web。
5. 冒烟（不真实发信）：4 个容器健康；公网 /health/ready、/login 200；未登录访问 /v1/inbox-messages、/v1/transactional-messages 为 401；四个容器日志 3 分钟内无 error/panic。
6. 冒烟失败：先停用部署后新建的测试 API key（如有），恢复上一个 compose 备份、换回旧镜像，确认恢复健康，然后停下等我。
7. 部署记录追加到 docs/DEPLOYMENT_FE5_0083.md。
8. 部署互斥（另有稳定性循环会话可能热修部署）：部署前 `sudo -n mkdir /opt/zmail/deploy/.deploy-lock`，失败就等对方结束；部署结束（成功或回滚）后 `sudo -n rmdir /opt/zmail/deploy/.deploy-lock`。

五、绝对不做
- 不用 Postgres 管理员账号；不打印、不记录、不提交任何密码、密钥、.env、带插值的 compose config。
- 不删除任何线上资源（容器、镜像、数据、DNS、Cloudflare 资源）；只能删自己当晚建的临时预演容器/网络。
- 不花钱；不创建新凭据或账号；不使用生产 Cloudflare 凭据（zmail-fe5-cloudflare.json）做测试；不接入现有生产域名。
- 不真实发信；不在生产 zmail.im 用密码登录；飞书后台只读。
- 不动 zgi-shared-postgres 的实例级配置，不碰其它项目的容器（共享库连接数接近上限的问题只记录，不处理）。
- 不用裸 `git stash`；不改写 Git 历史；不回滚别人的未提交改动；不强推（force push）。
- 源码只用英文，品牌 Zmail，数据库只用 PostgreSQL。

六、停下等我的情况（写清楚原因和需要我做什么，然后结束循环）
- 冒烟失败且已回滚；或回滚本身失败。
- 下一步需要钱、新凭据、外部账号、或删除线上资源。
- 两个方案差异重大且没有明显更优的产品决策。
- 全部可做的项都已完成。
结束时在日志里写总结：完成了哪些（PR 链接、部署的镜像标签）、没完成的及原因、我醒来需要决定的事项。

七、状态看板
- 每轮按 /Users/stark/item/zmail/.claude/status-board.md 更新状态看板（正在做、待我决定、切片、部署、巡检），并先执行用户在看板上已做的决定。进度只写看板，不在对话里播报。
