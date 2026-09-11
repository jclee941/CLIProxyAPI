from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[1]


class SecretTemplateTests(unittest.TestCase):
    def test_secret_files_are_op_inject_expressions_not_literal_references(self) -> None:
        expression = re.compile(r"\{\{\s*(op://[^\s{}]+)\s*\}\}")
        for filename in ("admin-key.tpl", "data-key.tpl"):
            with self.subTest(filename=filename):
                source = (ROOT / filename).read_text().strip()
                self.assertIsNotNone(
                    expression.fullmatch(source),
                    "op inject must materialize the secret instead of storing a public reference as the key",
                )


if __name__ == "__main__":
    unittest.main()
