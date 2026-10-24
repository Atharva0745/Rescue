import json
import os
import tempfile
import unittest

from technical_report_generator import (
    RESULTS_PATH,
    generate_technical_report,
)


class TechnicalReportGeneratorTest(unittest.TestCase):
    def test_generates_exploratory_report_with_provenance(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = os.path.join(temp_dir, "report.json")
            report = generate_technical_report(output_path=output_path)

            with open(output_path, "r", encoding="utf-8") as source:
                stored_report = json.load(source)

        self.assertEqual(report, stored_report)
        self.assertEqual(report["meta"]["status"], "EXPLORATORY_NOT_CONFIRMATORY")
        self.assertEqual(report["meta"]["n_seeds"], 25)
        self.assertEqual(report["database_validation"]["status"], "NOT_ESTABLISHED_BY_THIS_RERUN")
        self.assertTrue(report["provenance"]["results_sha256"])
        self.assertEqual(
            report["provenance"]["results_file"],
            os.path.relpath(RESULTS_PATH, os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))),
        )

    def test_refuses_registered_result_as_exploratory_input(self):
        registered_path = os.path.join(
            os.path.dirname(os.path.dirname(__file__)),
            "config",
            "confirmatory_results_repaired_confirmatory.json",
        )
        with tempfile.TemporaryDirectory() as temp_dir:
            output_path = os.path.join(temp_dir, "report.json")
            with self.assertRaisesRegex(ValueError, "not explicitly marked exploratory"):
                generate_technical_report(
                    results_path=registered_path,
                    output_path=output_path,
                )
            self.assertFalse(os.path.exists(output_path))


if __name__ == "__main__":
    unittest.main()
