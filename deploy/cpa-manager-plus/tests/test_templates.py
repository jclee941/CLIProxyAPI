import json
from pathlib import Path
from typing import cast
import unittest


ROOT = Path(__file__).resolve().parents[1]


class SetupTemplateTests(unittest.TestCase):
    def test_setup_requires_an_operator_supplied_existing_management_key(self) -> None:
        config = cast(dict[str, object], json.loads((ROOT / "setup.template.json").read_text()))
        self.assertEqual(config["cpaManagementKey"], "")
        self.assertEqual(config["cpaBaseUrl"], "http://cliproxyapi:8317")
        self.assertEqual(config["collectorMode"], "http")
        self.assertIs(config["requestMonitoringEnabled"], False)
        self.assertIs(config["ensureUsageStatisticsEnabled"], False)

    def test_obsolete_op_templates_are_not_shipped(self) -> None:
        for filename in ("admin-key.tpl", "data-key.tpl"):
            with self.subTest(filename=filename):
                self.assertFalse((ROOT / filename).exists())


if __name__ == "__main__":
    _ = unittest.main()
