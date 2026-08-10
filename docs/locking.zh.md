<p align="right"><a href="locking.md">English</a> | <strong>简体中文</strong></p>

# 运行时租约锁

每台 host 在 `/run/lock/alpineform/lock` 使用独占运行时租约。该路径位于 `/run` 下，因此
重启绝不会保留陈旧 lock。

获取租约时使用原子目录创建，并记录随机的 128 位 owner token 和 epoch 到期时间。租约有效时，
竞争者返回 busy。过期目录会先从 lock 路径重命名移走，然后新的 owner 才创建 lock，因此接管
陈旧租约时仍然只有一个胜者。

renew 和 release 操作会比较 owner token，并拒绝已过期或已被替换的租约。客户端按照 TTL 的
三分之一周期续约。获取租约遵守请求的 timeout 和 context cancellation；工作成功、失败或被取消后，
`WithLease` 使用有界的后台 context 执行释放。unlock 期间的传输失败会作为错误返回，并且同一
租约可以重试。
