import assert from "node:assert/strict";
import test from "node:test";
import { modelProtocolChoiceGroups, modelProtocolLabel, modelSelectionPayload } from "./model-protocol-choices.ts";
import { findModelProviderPreset } from "../../../entities/model/lib/model-provider-presets.ts";

test("DeepSeek Responses keeps its identity in provider and model selection", () => {
  assert.equal(findModelProviderPreset("https://api.deepseek.com/v1").protocol, "deepseek_responses");
  assert.equal(modelProtocolLabel(["deepseek_responses"]), "Responses (DeepSeek)");
  const protocols = ["openai_responses", "deepseek_responses"];
  assert.deepEqual(modelProtocolChoiceGroups(protocols).map((group) => group.vendor), ["OpenAI", "DeepSeek"]);
  assert.deepEqual(modelSelectionPayload([{ name: "deepseek-v4-pro" }], new Map([["deepseek-v4-pro", ["deepseek_responses"]]]), protocols), [
    { name: "deepseek-v4-pro", protocols: ["deepseek_responses"] },
  ]);
});
