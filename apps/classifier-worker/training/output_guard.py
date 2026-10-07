from pathlib import Path


def create_output_directory(out: Path) -> None:
    try:
        out.mkdir(parents=True, exist_ok=False)
    except FileExistsError:
        raise SystemExit(f"Output already exists: {out}. Choose a new --out directory.") from None
