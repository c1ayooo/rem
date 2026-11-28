# rem — 4zrcin 的隧道与中继

## 这个仓是什么

[chainreactors/rem](https://github.com/chainreactors/rem) 的 fork，模块路径仍是 `github.com/chainreactors/rem`。4zrcin 的远程节点经它回连控制台。主线是 `feat/remote-nodes`，`master` 与它同步。在 4zrcin 里的位置见 [仓库地图](https://github.com/c1ayooo/4zrcin#仓库地图)。

## 动手前必读

上游的 `README.md`；本 fork 相对自己 `master` 加了什么：`git log --oneline master..feat/remote-nodes`。

## 硬规则

- 上游加纯新增。改上游已有的文件之前，先想能不能放进新文件。
- 改导出面之前看两个消费方：外壳仓的 `cmd/rem-relay` 和带 `remtunnel` 标签的代码。它们经外壳 `go.mod` 里的 `replace` 用这里，不会被编译器提前拦住。
- 同步上游在 `feat/remote-nodes` 上做：`git fetch upstream` 后合并 `upstream/master`；通过后把 `master` 快进到同一处。

## 取向

同上游 cyber-harness 的 [AGENTS.md](https://github.com/chainreactors/cyber-harness/blob/master/AGENTS.md)：宁删不加抽象。

## 交付纪律

- 提交前跑 `go build ./...` 和受影响包的 `go test`。全量 `go test ./...` 里有要等超时的包，跑得久是正常的。
- 提交信息说清为什么改。依赖网络的测试失败如实报告，不算通过。
