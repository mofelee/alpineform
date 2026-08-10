<p align="right"><a href="openrc.md">English</a> | <strong>简体中文</strong></p>

# OpenRC 服务

AlpineForm 将受限的 init 脚本生成与运行时服务收敛分开。主机级 `openrc` 块通过
`files.file` 使用的同一个原子文件 provider 生成常见 `openrc-run` 脚本：

```hcl
host "edge" {
  openrc {
    service "worker" {
      description        = "Example background worker"
      command            = "/usr/local/bin/worker"
      command_args       = ["--listen", "127.0.0.1:9000"]
      command_user       = "worker"
      directory          = "/srv/worker"
      command_background = true
      pidfile            = "/run/worker.pid"
      need               = ["net"]
      use                = ["logger"]
      conf               = "WORKERS=2\n"
    }
  }

  services {
    service "worker" {
      enabled  = true
      runlevel = "default"
      state    = "running"
      operation = "restarted"
    }
  }
}
```

生成器拥有可执行模式为 `0755` 的 `/etc/init.d/<name>`；当 `conf` 非空时，还拥有模式
为 `0644` 的 `/etc/conf.d/<name>`。两者都使用 root 所有的原子文件替换，修复内容、
模式和所有权漂移，并在移除声明时默认仅忘记状态。

## 结构化边界

v0.1 生成器仅接受：

- `command`、`command_args`、`command_user` 和 `directory`
- `command_background` 和 `pidfile`
- `description`
- `need`、`use`、`want`、`after` 和 `before`
- 简单的字面量 `conf` 内容

command、directory 和 pidfile 必须是整洁的绝对路径。service、account 和 dependency
名称使用受限的 Alpine/OpenRC 标识符。后台 command 要求 pidfile。参数和生成的赋值
采用确定性的 POSIX 单引号转义；值绝不会成为生成的 shell 语法。

任意 shell 函数、start/stop hook、额外 command、多服务展开、自定义 runlevel 堆叠和
supervisor 特定程序都不属于此模型。请使用位于 `/etc/init.d/<name>` 的 `files.file`
以及可选的 `/etc/conf.d/<name>` 显式管理这些完整脚本。

运行时 enablement、runlevel 成员关系和 start/stop 行为由独立的
`services.service` 资源提供。

## 运行时收敛

`services.service` 观察已有的可执行 `/etc/init.d/<name>`，使用 `rc-update` 管理所选
runlevel 的成员关系，并使用 `rc-service` 管理运行时状态：

```hcl
host "edge" {
  services {
    service "worker" {
      enabled  = true
      runlevel = "default"
      state    = "running"

      package = "worker-daemon"
      user    = "worker"
      group   = "worker"
    }
  }
}
```

`enabled` 默认为 `true`，`runlevel` 默认为 `default`，`state` 默认为 `running`。
运行时状态为 `running` 或 `stopped`。检查期间会对 missing、inactive、started、stopped
和 crashed 服务进行分类；missing 或 crashed 服务无法满足 running 声明。更改托管
runlevel 时，会先从先前托管的 runlevel 移除服务，再应用新的成员关系；不相关的
runlevel 成员关系不会被视为 authoritative。

可选的 `operation = "restarted"` 或 `"reloaded"` 会在一个或多个匹配的托管 init/conf
文件确实发生变化后运行一次。同一次 apply 中的 init 和 conf 变化会聚合为一次服务
操作，而 no-op 或仅修复 runlevel 不会 restart/reload。作者声明的 `depends_on` 边也
不会激活该操作。operation 要求 `state = "running"`，并且至少有一个托管的
`/etc/init.d/<name>` 或 `/etc/conf.d/<name>` 触发器。OpenRC 始终支持 restart。只有
原始 init 脚本允许 reload；生成的脚本会在验证阶段拒绝 reload，而没有 reload command
的原始脚本会让 apply 失败并返回明确错误。原始脚本必须通过
`extra_started_commands` 声明 reload，并且应设置 `description_reload`。provider 在调用
reload 前检查 OpenRC 描述的 command，因为 OpenRC 对未定义的 fallback 也可能返回成功。

可选的 `package`、`user` 和 `group` 字段必须命名同一主机上声明为 present 的资源，
并使 service 依赖它们。生成的 service 还依赖其 init 和 conf 文件。当 `command_user`
命名一个声明为 present 的 user 时，会推断出该依赖。通过 `files.file` 在匹配 init/conf
路径管理的原始脚本会获得相同的文件顺序。

这些是推断出的前置资源。运行时 `services.service` 声明还可以通过 `depends_on` 添加
静态、同一作用域的 `packages.package`、`files.file` 或 `services.service` 引用，例如：

```hcl
services {
  service "worker" {
    package    = "worker-daemon"
    operation  = "restarted"
    depends_on = [file["/etc/conf.d/worker"]]
  }
}
```

生成的 `openrc.service` 声明本身既不接受资源 `depends_on`，也不会成为类型化的
`service.<label>` 目标。其生成的 init/conf 文件和匹配的运行时 service 会获得推断的
graph 边。

有效 plan 顺序结合结构、推断和作者声明的依赖，但只有匹配的 init/conf 变化会出现在
service 的活动 `triggered_by` 中并触发 restart。当 file 和 package 声明为受支持的显式
absence，同时 service 声明为 stopped 和 disabled 时，作者声明的顺序会把 service 工作
排在 file 删除之前，并把 file 删除排在 package 删除之前。依赖不会添加 service 删除
策略。移除 service 声明仍为 forget-only，不会执行 stop 或 disable；默认移除 file/package
声明也不会执行远端删除。

## 四 branch 工作流证明

阻塞式 `openrc` VM case 在 Alpine 3.21、3.22、3.23 和 3.24 上运行。其
package -> managed configuration -> service 工作流证明首次 apply、JSON no-op 和干净
check、漂移检测与修复、依赖方优先的显式清理，以及默认 forget，且无需增加第十三个
case。因此完整 suite 仍为 12 个 case、48 个 job；详见
[集成运行手册](../test/integration/libvirt/README.zh.md)。

移除 service 声明只会忘记其 state 条目。要 stop 或 disable 服务，请在移除声明前先
声明该意图。service 和 runlevel 名称使用受限的 OpenRC 标识符，provider 将它们作为
位置参数传递给固定脚本，而不是插值到 shell 文本中。
