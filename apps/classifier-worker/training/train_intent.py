#!/usr/bin/env python3
"""Train and export an astrlink-text-v1 intent classifier bundle.

Frozen multilingual sentence encoder + masked mean pooling + linear head,
exported to ONNX (int8) with the exact I/O the classifier worker expects.

    pip install --index-url https://download.pytorch.org/whl/cpu torch
    pip install "transformers<5" onnx onnxruntime==1.23.2 onnxscript sentencepiece
    python train_intent.py --out <bundle-dir>

The output directory is what the Core local probe/install accepts. Keep it
outside the repository: scripts/check-no-production-models.mjs rejects .onnx.
"""

import argparse
import hashlib
import json
import random
import unicodedata
from pathlib import Path

from output_guard import create_output_directory

import numpy as np
import onnxruntime as ort
import torch
from onnxruntime.quantization import QuantType, quantize_dynamic
from tokenizers import Tokenizer
from transformers import AutoConfig, AutoModel, AutoTokenizer

LABELS = ["general", "research", "coding", "architect"]
TAXONOMY_SHA256 = "83990d9d763df0feac9b06aef36ef14417a662494f53004e68c3f18248fd5a2f"
CONTENT_BUDGET, HEAD, TAIL, PAD_ID, PAD_MULTIPLE = 510, 255, 255, 0, 8
HERE = Path(__file__).resolve().parent


def normalize(text: str) -> str:
    # ponytail: skips the worker's 64 KiB byte window; training texts are short.
    return unicodedata.normalize("NFC", text).replace("\r\n", "\n").strip()


def encode(tokenizer: Tokenizer, text: str) -> list[int]:
    ids = tokenizer.encode(normalize(text), add_special_tokens=False).ids
    if len(ids) > CONTENT_BUDGET:
        ids = ids[:HEAD] + ids[-TAIL:]
    return ids


def batch(tokenizer: Tokenizer, texts: list[str]) -> tuple[torch.Tensor, torch.Tensor]:
    encoded = [encode(tokenizer, text) for text in texts]
    width = max(len(ids) for ids in encoded)
    width += -width % PAD_MULTIPLE
    ids = torch.full((len(encoded), width), PAD_ID, dtype=torch.long)
    mask = torch.zeros((len(encoded), width), dtype=torch.long)
    for row, values in enumerate(encoded):
        ids[row, : len(values)] = torch.tensor(values)
        mask[row, : len(values)] = 1
    return ids, mask


class Classifier(torch.nn.Module):
    def __init__(self, encoder: torch.nn.Module, hidden: int):
        super().__init__()
        self.encoder = encoder
        self.head = torch.nn.Linear(hidden, len(LABELS))

    def embed(self, input_ids: torch.Tensor, attention_mask: torch.Tensor) -> torch.Tensor:
        states = self.encoder(input_ids=input_ids, attention_mask=attention_mask).last_hidden_state
        weights = attention_mask.unsqueeze(-1).to(states.dtype)
        return (states * weights).sum(1) / weights.sum(1).clamp(min=1e-6)

    def forward(self, input_ids: torch.Tensor, attention_mask: torch.Tensor) -> torch.Tensor:
        return self.head(self.embed(input_ids, attention_mask))


def load_rows(path: Path) -> list[dict]:
    rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    for row in rows:
        if row["label"] not in LABELS:
            raise SystemExit(f"unknown label {row['label']!r} in {path}")
    return rows


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def macro_f1(truth: list[int], predicted: list[int]) -> tuple[float, list[float]]:
    scores = []
    for label in range(len(LABELS)):
        tp = sum(t == label and p == label for t, p in zip(truth, predicted))
        fp = sum(t != label and p == label for t, p in zip(truth, predicted))
        fn = sum(t == label and p != label for t, p in zip(truth, predicted))
        scores.append(0.0 if tp == 0 else 2 * tp / (2 * tp + fp + fn))
    return sum(scores) / len(scores), scores


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="sentence-transformers/paraphrase-multilingual-MiniLM-L12-v2")
    parser.add_argument("--out", type=Path, default=Path.home() / "astrlink-models" / "astrlink-intent-v1")
    parser.add_argument("--epochs", type=int, default=300)
    parser.add_argument("--seed", type=int, default=7)
    args = parser.parse_args()
    random.seed(args.seed)
    torch.manual_seed(args.seed)

    out: Path = args.out
    create_output_directory(out)
    AutoTokenizer.from_pretrained(args.base).save_pretrained(out)
    for extra in ("sentencepiece.bpe.model", "vocab.txt", "tokenizer_config.json", "special_tokens_map.json"):
        (out / extra).unlink(missing_ok=True)
    tokenizer = Tokenizer.from_file(str(out / "tokenizer.json"))
    tokenizer.no_padding()
    tokenizer.no_truncation()

    encoder = AutoModel.from_pretrained(args.base).eval()
    for parameter in encoder.parameters():
        parameter.requires_grad_(False)
    model = Classifier(encoder, encoder.config.hidden_size)

    train = load_rows(HERE / "train.jsonl")
    holdout = load_rows(HERE / "holdout.jsonl")
    with torch.no_grad():
        features = torch.cat([model.embed(*batch(tokenizer, [r["text"] for r in train[i : i + 32]])) for i in range(0, len(train), 32)])
    targets = torch.tensor([LABELS.index(r["label"]) for r in train])

    optimizer = torch.optim.AdamW(model.head.parameters(), lr=1e-2, weight_decay=1e-2)
    for _ in range(args.epochs):
        optimizer.zero_grad()
        loss = torch.nn.functional.cross_entropy(model.head(features), targets)
        loss.backward()
        optimizer.step()
    print(f"train loss {loss.item():.4f}")

    fp32 = out / "model.fp32.onnx"
    sample = batch(tokenizer, ["export sample"])
    torch.onnx.export(
        model.eval(),
        sample,
        str(fp32),
        input_names=["input_ids", "attention_mask"],
        output_names=["logits"],
        dynamic_axes={"input_ids": {0: "batch", 1: "sequence"}, "attention_mask": {0: "batch", 1: "sequence"}, "logits": {0: "batch"}},
        opset_version=17,
        dynamo=False,
    )
    quantize_dynamic(str(fp32), str(out / "model.onnx"), weight_type=QuantType.QInt8)
    fp32.unlink()

    config = AutoConfig.from_pretrained(args.base).to_dict()
    config.update(
        architectures=[f"{config.get('model_type', 'bert').capitalize()}ForSequenceClassification"],
        id2label={str(i): label for i, label in enumerate(LABELS)},
        label2id={label: i for i, label in enumerate(LABELS)},
        astrlink_pooling="masked_mean",
        astrlink_base_model=args.base,
    )
    (out / "config.json").write_text(json.dumps(config, ensure_ascii=False, indent=2), encoding="utf-8")

    session = ort.InferenceSession(str(out / "model.onnx"), providers=["CPUExecutionProvider"])
    predicted = []
    for row in holdout:
        ids, mask = batch(tokenizer, [row["text"]])
        logits = session.run(["logits"], {"input_ids": ids.numpy(), "attention_mask": mask.numpy()})[0][0]
        predicted.append(int(np.argmax(logits)))
    truth = [LABELS.index(r["label"]) for r in holdout]
    macro, per_class = macro_f1(truth, predicted)
    accuracy = sum(t == p for t, p in zip(truth, predicted)) / len(truth)
    print(f"holdout n={len(truth)} accuracy={accuracy:.3f} macro_f1={macro:.3f}")
    for label, score in zip(LABELS, per_class):
        print(f"  {label:<10} f1={score:.3f}")
    for row, t, p in zip(holdout, truth, predicted):
        if t != p:
            print(f"  miss: {LABELS[t]} -> {LABELS[p]}: {row['text']}")

    files = {name: {"sha256": sha256(out / name), "size_bytes": (out / name).stat().st_size} for name in ("model.onnx", "tokenizer.json", "config.json")}
    manifest = {
        "artifact_tier": "experimental",
        "source": {"artifact_tier": "experimental", "release_mode": "experimental", "taxonomy_sha256": TAXONOMY_SHA256, "base_model": args.base},
        "contract": {"id2label": {str(i): label for i, label in enumerate(LABELS)}},
        "files": files,
        "holdout": {"n": len(truth), "accuracy": round(accuracy, 4), "macro_f1": round(macro, 4)},
    }
    (out / "onnx-manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
    print(f"bundle: {out}")


if __name__ == "__main__":
    main()
