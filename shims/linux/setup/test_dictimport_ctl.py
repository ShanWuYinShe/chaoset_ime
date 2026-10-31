#!/usr/bin/env python3
# dictimport_ctl 单测：无 GTK、无 ibus、无真实 chaoset-import。
#
# 通过替换依赖（find_tool / _run / _restart_engines）验证：参数拼接（已有词库
# 作为底表）、临时文件 + 备份 + 原子替换、失败时旧词库不变、重启失败提示。

import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import dictimport_ctl as di  # noqa: E402

TOOL = "/usr/bin/chaoset-import"


def _proc(code=0, out="", err=""):
    return mock.Mock(returncode=code, stdout=out, stderr=err)


class DictimportCtlTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.addCleanup(lambda: __import__("shutil").rmtree(self.dir, ignore_errors=True))
        self.target = os.path.join(self.dir, "data", "dict.tsv")
        self.calls = []
        self.run_patch = mock.patch.object(di, "_run", side_effect=self._fake_run)
        self.run_patch.start()
        self.addCleanup(self.run_patch.stop)
        self.tool_patch = mock.patch.object(di, "find_tool", return_value=TOOL)
        self.tool_patch.start()
        self.addCleanup(self.tool_patch.stop)
        self.restart_patch = mock.patch.object(di, "_restart_engines",
                                               return_value=(True, "ibus 已重启"))
        self.restart_patch.start()
        self.addCleanup(self.restart_patch.stop)

    def _fake_run(self, args, timeout=60):
        self.calls.append(list(args))
        # 模拟 chaoset-import：-o 指向的输出文件写出内容
        out = args[args.index("-o") + 1]
        os.makedirs(os.path.dirname(out), exist_ok=True)
        with open(out, "w", encoding="utf-8") as fh:
            fh.write("测试词\tce shi ci\t99\n")
        return _proc(out="已写入 %s（共 1 条）\n" % out)

    def test_imported_dict_path_respects_xdg(self):
        with mock.patch.dict(os.environ, {"XDG_DATA_HOME": "/tmp/xdg"}):
            self.assertEqual(di.imported_dict_path(), "/tmp/xdg/chaoset/dict.tsv")

    def test_import_requires_files(self):
        ok, text = di.import_files([], target=self.target)
        self.assertFalse(ok)
        self.assertIn("未选择", text)
        self.assertEqual(self.calls, [])

    def test_import_tool_missing(self):
        with mock.patch.object(di, "find_tool", return_value=None):
            ok, text = di.import_files(["/in.yaml"], target=self.target)
        self.assertFalse(ok)
        self.assertIn("chaoset-import", text)

    def test_import_creates_target_and_passes_args(self):
        ok, text = di.import_files(["/in.yaml"], target=self.target)
        self.assertTrue(ok, text)
        self.assertTrue(os.path.isfile(self.target))
        args = self.calls[0]
        self.assertEqual(args[:3], [TOOL, "-o", self.target + ".tmp"])
        self.assertNotIn(self.target, args)  # 首次导入没有底表
        self.assertEqual(args[-1], "/in.yaml")

    def test_import_uses_existing_target_as_base(self):
        os.makedirs(os.path.dirname(self.target), exist_ok=True)
        with open(self.target, "w", encoding="utf-8") as fh:
            fh.write("旧词\tjiu ci\t10\n")
        ok, _ = di.import_files(["/in.yaml"], target=self.target)
        self.assertTrue(ok)
        args = self.calls[0]
        self.assertIn(self.target, args)
        # 备份存在
        baks = [n for n in os.listdir(os.path.dirname(self.target))
                if ".bak." in n]
        self.assertEqual(len(baks), 1)

    def test_import_tool_failure_keeps_old_target(self):
        os.makedirs(os.path.dirname(self.target), exist_ok=True)
        with open(self.target, "w", encoding="utf-8") as fh:
            fh.write("旧词\tjiu ci\t10\n")
        old = Path(self.target).read_text(encoding="utf-8")
        with mock.patch.object(di, "_run", return_value=_proc(code=1, err="坏词库")):
            ok, text = di.import_files(["/in.yaml"], target=self.target)
        self.assertFalse(ok)
        self.assertIn("坏词库", text)
        self.assertEqual(Path(self.target).read_text(encoding="utf-8"), old)

    def test_import_restart_failure_reports_imported(self):
        with mock.patch.object(di, "_restart_engines",
                               return_value=(False, "ibus 未能自动重启")):
            ok, text = di.import_files(["/in.yaml"], target=self.target)
        self.assertFalse(ok)
        self.assertIn("词库已导入", text)
        self.assertIn("ibus", text)


if __name__ == "__main__":
    unittest.main()