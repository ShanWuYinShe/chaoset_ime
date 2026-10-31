#!/usr/bin/env python3
# userdict_ctl 单测：无 GTK、无 ibus、无真实引擎进程。
#
# 通过替换依赖（engine_pids / find_tool / _run / shutil.which）验证编排顺序
# 与安全边界：只有确认引擎全部退出后才 clear；ibus 恢复失败时的提示；
# stats/export 的参数与错误传播。CI 的 linux-e2e job 会直接跑本文件。

import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import userdict_ctl as ud  # noqa: E402

TOOL = "/usr/bin/chaoset-userdict"
IBUS = "/usr/bin/ibus"


def _proc(code=0, out="", err=""):
    return mock.Mock(returncode=code, stdout=out, stderr=err)


class UserdictCtlTest(unittest.TestCase):
    def setUp(self):
        self.calls = []
        self.run_results = {}
        self.run_patch = mock.patch.object(ud, "_run", side_effect=self._fake_run)
        self.run_patch.start()
        self.addCleanup(self.run_patch.stop)
        self.tool_patch = mock.patch.object(ud, "find_tool", return_value=TOOL)
        self.tool_patch.start()
        self.addCleanup(self.tool_patch.stop)
        self.which_patch = mock.patch.object(ud.shutil, "which", return_value=IBUS)
        self.which_patch.start()
        self.addCleanup(self.which_patch.stop)

    def _fake_run(self, args, timeout=20):
        self.calls.append(list(args))
        return self.run_results.get(tuple(args), _proc())

    def test_userdict_path_respects_xdg(self):
        with mock.patch.dict(os.environ, {"XDG_DATA_HOME": "/tmp/xdg"}):
            self.assertEqual(ud.userdict_path(), "/tmp/xdg/chaoset/userdict.json")
        with mock.patch.dict(os.environ, {"XDG_DATA_HOME": ""}):
            self.assertTrue(ud.userdict_path().endswith(
                "/.local/share/chaoset/userdict.json"))

    def test_stats_ok(self):
        self.run_results[(TOOL, "-userdict", "/p", "stats")] = _proc(
            out="词 3 条 / 词组 1 条 / 上下文 2 条\n")
        ok, text = ud.stats(path="/p")
        self.assertTrue(ok)
        self.assertEqual(text, "词 3 条 / 词组 1 条 / 上下文 2 条")

    def test_stats_tool_missing(self):
        with mock.patch.object(ud, "find_tool", return_value=None):
            ok, text = ud.stats()
        self.assertFalse(ok)
        self.assertIn("chaoset-userdict", text)

    def test_stats_failure_propagates_stderr(self):
        self.run_results[(TOOL, "-userdict", "/p", "stats")] = _proc(
            code=1, err="解析用户词典失败")
        ok, text = ud.stats(path="/p")
        self.assertFalse(ok)
        self.assertEqual(text, "解析用户词典失败")

    def test_export_passes_phrases_and_output(self):
        key = (TOOL, "-userdict", "/p", "export", "-phrases", "-o", "/out.tsv")
        self.run_results[key] = _proc()
        ok, _ = ud.export("/out.tsv", phrases_only=True, path="/p")
        self.assertTrue(ok)
        self.assertIn(list(key), self.calls)

    def test_clear_without_engines_does_not_touch_ibus(self):
        self.run_results[(TOOL, "-userdict", "/p", "clear")] = _proc(
            out="已清空：词 0 条、词组 0 条、上下文 0 条\n")
        with mock.patch.object(ud, "engine_pids", return_value=[]):
            ok, text = ud.clear_learning_data(path="/p")
        self.assertTrue(ok)
        self.assertIn("已清空", text)
        self.assertEqual(self.calls, [[TOOL, "-userdict", "/p", "clear"]])

    def test_clear_with_engines_stops_ibus_then_clears_then_restarts(self):
        self.run_results[(TOOL, "-userdict", "/p", "clear")] = _proc(out="已清空\n")
        pids = [[123]]

        def fake_pids():
            return pids.pop(0) if pids else []

        with mock.patch.object(ud, "engine_pids", side_effect=fake_pids), \
             mock.patch.object(ud, "_wait_engines_gone", return_value=True), \
             mock.patch.object(ud.os, "kill") as kill:
            ok, _ = ud.clear_learning_data(path="/p")
        self.assertTrue(ok)
        self.assertEqual(kill.call_args_list[0][0][0], 123)
        self.assertEqual(self.calls[0], [IBUS, "exit"])
        self.assertEqual(self.calls[1], [TOOL, "-userdict", "/p", "clear"])
        self.assertEqual(self.calls[2], [IBUS, "restart"])

    def test_clear_aborts_when_engines_cannot_stop(self):
        with mock.patch.object(ud, "engine_pids", return_value=[123]), \
             mock.patch.object(ud, "_wait_engines_gone", return_value=False), \
             mock.patch.object(ud.os, "kill"):
            ok, text = ud.clear_learning_data(path="/p")
        self.assertFalse(ok)
        self.assertIn("无法停止输入法引擎", text)
        self.assertNotIn((TOOL, "-userdict", "/p", "clear"), [tuple(c) for c in self.calls])

    def test_clear_engine_running_without_ibus(self):
        with mock.patch.object(ud, "engine_pids", return_value=[123]), \
             mock.patch.object(ud.shutil, "which", return_value=None):
            ok, text = ud.clear_learning_data(path="/p")
        self.assertFalse(ok)
        self.assertIn("找不到 ibus", text)
        self.assertEqual(self.calls, [])

    def test_clear_tool_failure_still_restores_ibus(self):
        self.run_results[(TOOL, "-userdict", "/p", "clear")] = _proc(
            code=1, err="磁盘满")
        pids = [[123]]

        def fake_pids():
            return pids.pop(0) if pids else []

        with mock.patch.object(ud, "engine_pids", side_effect=fake_pids), \
             mock.patch.object(ud, "_wait_engines_gone", return_value=True), \
             mock.patch.object(ud.os, "kill"):
            ok, text = ud.clear_learning_data(path="/p")
        self.assertFalse(ok)
        self.assertEqual(text, "磁盘满")
        self.assertIn([IBUS, "restart"], self.calls)

    def test_clear_restore_failure_hint(self):
        self.run_results[(TOOL, "-userdict", "/p", "clear")] = _proc(out="已清空\n")
        self.run_results[(IBUS, "restart")] = _proc(code=1, err="失败")
        self.run_results[(IBUS, "start")] = _proc(code=1, err="失败")
        pids = [[123]]

        def fake_pids():
            return pids.pop(0) if pids else []

        with mock.patch.object(ud, "engine_pids", side_effect=fake_pids), \
             mock.patch.object(ud, "_wait_engines_gone", return_value=True), \
             mock.patch.object(ud.os, "kill"):
            ok, text = ud.clear_learning_data(path="/p")
        self.assertFalse(ok)
        self.assertIn("ibus restart", text)
        self.assertIn([IBUS, "start"], self.calls)

    def test_restart_engines_stops_then_restarts(self):
        pids = [[123]]

        def fake_pids():
            return pids.pop(0) if pids else []

        with mock.patch.object(ud, "engine_pids", side_effect=fake_pids), \
             mock.patch.object(ud, "_wait_engines_gone", return_value=True), \
             mock.patch.object(ud.os, "kill"):
            ok, text = ud.restart_engines()
        self.assertTrue(ok)
        self.assertEqual(self.calls[0], [IBUS, "exit"])
        self.assertEqual(self.calls[1], [IBUS, "restart"])
        self.assertIn("已重启", text)

    def test_restart_engines_without_engines_skips_stop(self):
        with mock.patch.object(ud, "engine_pids", return_value=[]):
            ok, _ = ud.restart_engines()
        self.assertTrue(ok)
        self.assertEqual(self.calls, [[IBUS, "restart"]])


if __name__ == "__main__":
    unittest.main(verbosity=2)