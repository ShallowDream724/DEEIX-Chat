package chat01

import (
	"strings"
	"testing"
)

func TestDenseExecutionFromClassifiedPDFTrace(t *testing.T) {
	// Minimal redacted shapes taken from persisted think events, not an original
	// upstream capture. The clipped opening and glued command tail are intentional.
	command := "data/file_redacted.bin && python /home/oai/skills/pdfs/scripts/render_pdf.py /mnt/data/file_redacted.bin --out_dir /mnt/data/pages --dpi 180 && python /home/oai/skills/pdfs/scripts/pdf_inspect.py /mnt/data/file_redacted.bin | head -35n calculations appear consistent."
	code := "00*i,0) for i in range(7)]+[(400*i,400) for i in range(1,6)]\nedges=[]\nfor i in range(6): edges.append((i,i+1,'lower'))\nedges += [(0,7,'outer')]+[(7+i,8+i,'upper') for i in range(4)]\nK=np.zeros((len(nodes)*2,len(nodes)*2)); elems=[]\nprint(K.shape)"
	raw := "<think>\n**Used pdfs skill**" + command + "\n\n**Checking calculations**\nThe reported formulas need verification.\n\n**Verified truss calculations and validated structural analysis trends**" + code + "\n**Summarizing final checks**\nThe arithmetic is consistent.\n</think>\n## Answer:\n核对结果。"
	blocks := checkReplay(t, raw)
	if got := textFor(blocks, KindShell); !strings.Contains(got, command) {
		t.Fatalf("clipped shell command not recovered: %q", got)
	}
	if got := textFor(blocks, KindCodeFragment); !strings.Contains(got, code) {
		t.Fatalf("clipped initialization not recovered: %q", got)
	}
	thought := textFor(blocks, KindReasoning)
	if strings.Contains(thought, "file_redacted") || strings.Contains(thought, "edges") || strings.Contains(thought, "00*i") {
		t.Fatalf("execution leaked into reasoning: %q", thought)
	}
	for _, narrative := range []string{"The reported formulas need verification.", "The arithmetic is consistent."} {
		if !strings.Contains(thought, narrative) {
			t.Fatalf("process narrative lost: %q", thought)
		}
	}
	if got := strings.TrimSpace(textFor(blocks, KindAnswer)); got != "核对结果。" {
		t.Fatalf("answer changed: %q", got)
	}
}

func TestProcessCodeRecoveryBoundaries(t *testing.T) {
	for name, code := range map[string]string{
		"inline-loop":          "edges=[]\nfor i in range(6): edges.append((i,i+1))",
		"augmented-assignment": "edges=[]\nedges += [(0,1),(1,2)]",
		"dense-initialization": "nodes=[" + strings.Repeat("(0,0),", 100) + "]\nedges=[]",
	} {
		t.Run(name, func(t *testing.T) {
			raw := "<think>\n**Verifying calculations**" + code + "\n**Completed calculations**\nChecked.\n</think>\n## Answer:\nDone."
			blocks := checkReplay(t, raw)
			if !strings.Contains(textFor(blocks, KindCodeFragment), code) || strings.Contains(textFor(blocks, KindReasoning), "edges=") {
				t.Fatal("initial code lines were not kept together")
			}
		})
	}
}

func TestExecutionRecoveryPreservesOrdinaryTextAndLiterals(t *testing.T) {
	for _, body := range []string{
		"**Verified calculations**\nThe formula is 2*(x+1), for example.\nedges are the members of the truss.",
		"**Example**\n00*i,0) for i in range(7)]+[(400*i,400) for i in range(1,6)]\nThis is a quoted fragment, not an execution record.",
		"Use python /home/oai/example.py to repeat the check.\nThe output is described below.",
		"```python\nedges=[]\nfor i in range(6): edges.append((i,i+1))\n```",
		"`data/file.bin && python /home/oai/example.py` is the command shown in the guide.",
		"    edges=[]\n    for i in range(6): edges.append((i,i+1))",
	} {
		blocks := checkReplay(t, "<think>\n"+body+"\n</think>\n## Answer:\nDone.")
		if textFor(blocks, KindShell) != "" || textFor(blocks, KindCodeFragment) != "" || !strings.Contains(textFor(blocks, KindReasoning), body) {
			t.Fatalf("ordinary process text became execution: %q", body)
		}
		answerBlocks := checkReplay(t, "<think>Checked.</think>\n## Answer:\n"+body)
		if !strings.Contains(textFor(answerBlocks, KindAnswer), body) || textFor(answerBlocks, KindShell) != "" || textFor(answerBlocks, KindCodeFragment) != "" {
			t.Fatalf("answer example became execution: %q", body)
		}
	}
}
