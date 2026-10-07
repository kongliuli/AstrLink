from pathlib import Path
from tempfile import TemporaryDirectory
import unittest

from output_guard import create_output_directory


class OutputGuardTest(unittest.TestCase):
    def test_existing_bundle_is_preserved_and_new_directory_is_created(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            create_output_directory(bundle)
            model = bundle / "model.onnx"
            model.write_bytes(b"existing model")
            with self.assertRaises(SystemExit):
                create_output_directory(bundle)
            self.assertEqual(model.read_bytes(), b"existing model")
            with self.assertRaises(SystemExit):
                create_output_directory(model)
            self.assertEqual(model.read_bytes(), b"existing model")
            fresh = root / "next" / "bundle"
            create_output_directory(fresh)
            self.assertTrue(fresh.is_dir())


if __name__ == "__main__":
    unittest.main()
