import assert from "node:assert/strict";
import {Chat01ReplayParser, resourcesFor} from "./normalizer.mjs";

export function verifyEdgeCases() {
  const examples = [
    ['tilde fence', '## Answer:\n~~~python\n## Thought:\n<think>\n~~~\n正文', 'answer'],
    ['nested backticks', '## Answer:\n````markdown\n```python\n## Thought:\n```\n````\n正文', 'answer'],
    ['inline literals', '## Answer:\n保留 `<think>`、`</think>`、`` `## Thought:` ``。', 'answer'],
    ['escaped marker', '## Answer:\n\\<think> 是转义后的标签。', 'answer'],
    ['indented code', '## Answer:\n    <think>\n    ## Thought:\n正文', 'answer'],
    ['python comment', '<think>\n## Python:\n# ## Answer:\nprint(1)\n</think>\n## Answer:\n真答案', 'python'],
    ['shell heredoc literals', '<think>\nbash -lc set -eu\ncat > out.txt <<\'END\'\n## Answer:\n<think>\nEND\ncat out.txt\n</think>\n## Answer:\n真答案', 'shell'],
    ['clipped code after thought', '<think>\n**Completed inspection**record.json\').read_text()\nassert count == 3\nprint(count)\n</think>\n## Answer:\n真答案', 'code-fragment'],
    ['incomplete marker', '## Answer:\n保留未完整标记 <thi', 'answer'],
    ['orphan bold suffix', '<think>\n损坏标题的尾部**\n</think>\n## Answer:\n真答案', 'reasoning'],
    ['oversized incomplete component', '## Answer:\ngenui'+'x'.repeat(66000)+'\n## Thought:\n组件内部文本', 'component'],
  ];
  for (const [name,raw,expected] of examples) {
    const baseline=new Chat01ReplayParser().feed(raw).finish();
    assert.equal(baseline.blocks.map(b=>b.text).join(''),raw,`${name}: source conservation`);
    assert.ok(baseline.blocks.some(b=>b.kind===expected),`${name}: expected classification`);
    if(expected==='answer')assert.equal(baseline.blocks.filter(b=>b.kind==='reasoning').length,0,`${name}: no false reasoning`);
    if(['shell','python','code-fragment'].includes(expected))assert.equal(baseline.blocks.filter(b=>b.kind==='answer').map(b=>b.text).join('').trim(),'真答案',`${name}: answer boundary`);
    for(const width of [1,5,127]) {
      const parser=new Chat01ReplayParser();
      for(let offset=0;offset<raw.length;offset+=width)parser.feed(raw.slice(offset,offset+width));
      parser.finish();
      assert.ok(JSON.stringify(parser.blocks)===JSON.stringify(baseline.blocks),`${name}: split ${width}`);
    }
    if(expected==='component') {
      const block=baseline.blocks.find(b=>b.kind==='component');
      assert.equal(block.complete,false); assert.equal(block.oversized,true);
      assert.equal(baseline.blocks.filter(b=>b.kind==='reasoning').length,0);
    }
  }
  // Heartbeats carry no text. Hours of empty events cannot end or alter a block.
  const longTask=new Chat01ReplayParser().feed('## Answer:\n已开始。');
  for(let second=15;second<=9000;second+=15)longTask.feed('');
  longTask.feed('<think>继续处理</think>\n## Answer:\n已完成。').finish();
  assert.ok(longTask.blocks.some(b=>b.kind==='answer'&&b.text.includes('已完成。')));
  const brokenLink='## Answer:\n[坏文件名](https://files.chat01.ai/a%ZZ) [坏地址](https://[invalid) [有效文件](https://files.chat01.ai/report.txt) 后续正文';
  const links=new Chat01ReplayParser().feed(brokenLink).finish();
  const resources=resourcesFor(links.blocks);
  assert.equal(resources.length,2);
  assert.equal(resources[0].name,'a%ZZ');
  assert.equal(resources[1].name,'report.txt');
  assert.equal(links.blocks.map(b=>b.text).join(''),brokenLink);
  return {cases:examples.length+1, simulatedHeartbeatSeconds:9000};
}
