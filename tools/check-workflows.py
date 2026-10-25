#!/usr/bin/env python3
"""校验 .github/workflows/*.yml 的结构完整性。

为什么需要这个脚本（2026-10-25 的事故）：
  给 package-macos 插入新步骤时，文本替换的范围覆盖过头，把紧随其后的
  package-linux / package-windows 两个 job 的头部整段删掉了，只留下它们的
  steps 残留在上一个 job 尾部。结果：
    - YAML 依然能被 yaml.safe_load 正常解析（语法没坏）；
    - 但 jobs 从 5 个变成 3 个，GitHub 直接判「workflow file issue」，
      整个 CI 跑不起来，三端打包与 Release 全部无法产出；
    - release.needs 引用了不存在的 job，纯 YAML 校验也不会报错。
  也就是说「yaml 能解析」远远不足以证明 workflow 可用，必须校验**结构**。

本脚本检查：
  1. 每个 workflow 必须有非空 jobs；
  2. 每个 job 必须有 runs-on 与 steps，且每个 step 有 uses 或 run；
  3. needs 引用的 job 必须真实存在（拼错/被删会在这里暴露）；
  4. 可选：用 --expect-jobs 断言关键 job 存在（防止被静默删掉）。

用法：
  python3 tools/check-workflows.py .github/workflows/ci.yml
  python3 tools/check-workflows.py --expect-jobs test,package-macos,package-linux,package-windows,release .github/workflows/ci.yml

退出码：0 = 全部通过，1 = 有问题。
"""
import sys
import os
import glob

try:
    import yaml
except ImportError:
    sys.exit("需要 PyYAML：pip install pyyaml")


def normalize_needs(needs):
    """把 needs 统一成列表。

    GitHub Actions 允许两种写法：
        needs: test
        needs: [a, b]
    早期版本直接对 needs 做 for 循环，字符串会被按字符迭代，于是
    needs: test 被报成「引用了不存在的 job: t / e / s / t」——
    校验脚本自己先出错，反而误导排查。必须显式区分字符串与列表。
    """
    if needs is None:
        return []
    if isinstance(needs, str):
        return [needs]
    return list(needs)


def check_file(path, expect_jobs):
    errors = []
    try:
        with open(path, encoding="utf-8") as f:
            doc = yaml.safe_load(f)
    except yaml.YAMLError as e:
        return [f"{path}: YAML 解析失败: {e}"]

    if not isinstance(doc, dict):
        return [f"{path}: 顶层不是映射"]

    jobs = doc.get("jobs")
    if not jobs:
        return [f"{path}: 没有 jobs（或为空）"]

    if expect_jobs:
        missing = [j for j in expect_jobs if j not in jobs]
        if missing:
            errors.append(f"{path}: 缺少预期 job: {', '.join(missing)}（现有: {', '.join(jobs)}）")

    for name, job in jobs.items():
        if not isinstance(job, dict):
            errors.append(f"{path}: job {name} 不是映射")
            continue

        runs_on = job.get("runs-on")
        if not runs_on:
            errors.append(f"{path}: job {name} 缺少 runs-on")

        steps = job.get("steps")
        if not steps:
            errors.append(f"{path}: job {name} 缺少 steps")
        else:
            for i, step in enumerate(steps):
                if not isinstance(step, dict):
                    errors.append(f"{path}: job {name} 第 {i+1} 个 step 不是映射")
                    continue
                if "uses" not in step and "run" not in step:
                    errors.append(
                        f"{path}: job {name} 第 {i+1} 个 step 既没有 uses 也没有 run"
                        f"（name={step.get('name')!r}）"
                    )

        for need in normalize_needs(job.get("needs")):
            if need not in jobs:
                errors.append(f"{path}: job {name} 的 needs 引用了不存在的 job: {need}")

    # needs 不能成环（简单检测：DFS）
    WHITE, GRAY, BLACK = 0, 1, 2
    color = {n: WHITE for n in jobs}

    def dfs(n, stack):
        color[n] = GRAY
        for need in normalize_needs((jobs.get(n) or {}).get("needs")):
            if need not in jobs:
                continue
            if color[need] == GRAY:
                errors.append(f"{path}: needs 存在环: {' -> '.join(stack + [need])}")
            elif color[need] == WHITE:
                dfs(need, stack + [need])
        color[n] = BLACK

    for n in list(jobs):
        if color[n] == WHITE:
            dfs(n, [n])

    return errors


def main(argv):
    expect_jobs = []
    files = []
    i = 1
    while i < len(argv):
        arg = argv[i]
        if arg == "--expect-jobs":
            i += 1
            if i >= len(argv):
                sys.exit("--expect-jobs 需要一个逗号分隔的 job 列表")
            expect_jobs = [x.strip() for x in argv[i].split(",") if x.strip()]
        elif arg in ("-h", "--help"):
            print(__doc__)
            return 0
        else:
            files.append(arg)
        i += 1

    if not files:
        files = sorted(glob.glob(".github/workflows/*.yml") + glob.glob(".github/workflows/*.yaml"))

    if not files:
        sys.exit("没有找到任何 workflow 文件")

    all_errors = []
    for path in files:
        if not os.path.isfile(path):
            all_errors.append(f"{path}: 文件不存在")
            continue
        errs = check_file(path, expect_jobs)
        if errs:
            all_errors.extend(errs)
        else:
            with open(path, encoding="utf-8") as f:
                jobs = list((yaml.safe_load(f).get("jobs") or {}).keys())
            print(f"ok   {path}（jobs: {', '.join(jobs)}）")

    if all_errors:
        print()
        for e in all_errors:
            print("FAIL " + e)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
