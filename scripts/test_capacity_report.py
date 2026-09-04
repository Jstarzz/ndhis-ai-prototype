import unittest

from capacity_report import error_rate, select


class CapacityReportTests(unittest.TestCase):
    def test_selects_highest_passing_concurrency(self):
        rows = [
            {"concurrency": 1, "ok": 10, "errors": 0, "p95": 0.8},
            {"concurrency": 2, "ok": 10, "errors": 0, "p95": 1.2},
            {"concurrency": 4, "ok": 9, "errors": 1, "p95": 1.5},
        ]
        result = select(rows, "p95", 2.0, 0.01)
        self.assertEqual(result["concurrency"], 2)

    def test_returns_none_when_thresholds_fail(self):
        rows = [{"concurrency": 1, "ok": 9, "errors": 1, "p95_ms": 3000}]
        self.assertIsNone(select(rows, "p95_ms", 2000, 0.01))

    def test_error_rate_handles_empty_sample(self):
        self.assertEqual(error_rate({"ok": 0, "errors": 0}), 1.0)


if __name__ == "__main__":
    unittest.main()
