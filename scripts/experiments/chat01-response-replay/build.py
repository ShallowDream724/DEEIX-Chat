"""Build an offline review from existing captures; never calls a provider."""
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
CAPTURES = ROOT / ".cache/chat01-probes/20261104"
CASES = [
    ("../20261105/stress-a/17_repeated_code", "多步代码修复｜故障复现、修复、测试与打包"),
    ("../20261105/stress-b/18_office_pdf_compare", "DOCX＋PPTX＋PDF｜多次执行与代码截片"),
    ("../20261105/stress-a/19_api_native_components", "原生组件｜地图、按钮、表格缺少数据"),
    ("../20261105/stress-b/20_literal_markers", "协议字面量｜行内代码与嵌套代码块"),
    ("../20261105/stress-a/21_audio_jsonl", "音频＋损坏数据｜残留 sandbox 下载链接"),
    ("03_mixed_image_search_code", "生图＋思考＋代码＋文件｜4 段 Answer"),
    ("02_search_code_files", "搜索＋命令＋文件｜正文保留代码示例"),
    ("05_search_map_cards", "地图组件｜缺少位置数据"),
    ("06_nonstream_code_files", "非流式返回｜粘连的命令结束标记"),
    ("01_native_image", "直接生图｜图片在 Answer 之前"),
]
items = []
for slug, name in CASES:
    folder = CAPTURES / slug
    if not (folder / "content.txt").is_file():
        continue
    raw = (folder / "content.txt").read_text(encoding="utf-8")
    frames = []
    for line in (folder / "events.jsonl").read_text(encoding="utf-8").splitlines():
        event = json.loads(line)
        try:
            data = json.loads(event["data"])
        except (ValueError, KeyError):
            continue
        choices = data.get("choices", [])
        if choices and "delta" in choices[0]:
            frames.append({"t": event.get("t", 0), "text": choices[0]["delta"].get("content") or ""})
    if frames:
        assert "".join(frame["text"] for frame in frames) == raw
    else:
        frames = [{"t": 0, "text": raw}]
    items.append({"name": name, "source": slug, "frames": frames})

code = (HERE / "normalizer.mjs").read_text(encoding="utf-8").replace("export ", "")
data = json.dumps(items, ensure_ascii=False, separators=(",", ":")).replace("<", "\\u003c")
html = (HERE / "preview.html").read_text(encoding="utf-8").replace("__NORMALIZER__", code).replace("__CASES__", data)
output = Path(sys.argv[1])
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(html, encoding="utf-8")
assert output.stat().st_size < 1_000_000
print(json.dumps({"path": str(output), "bytes": output.stat().st_size, "samples": len(items), "frames": [len(item["frames"]) for item in items]}, ensure_ascii=False))
