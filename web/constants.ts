export const MODEL_NAMES_STD = ["GeneZC/MiniChat-3B"];

export const MODEL_NAMES_AWQ = [
  "TheBloke/OpenHermes-2.5-Mistral-7B-16k-AWQ",
  "TheBloke/Xwin-LM-13B-v0.2-AWQ",
  "TheBloke/XwinCoder-13B-AWQ",
  "TheBloke/CodeLlama-13B-Instruct-AWQ",
  "TheBloke/Llama-2-13B-chat-AWQ",
  "TheBloke/Orca-2-13B-AWQ",
];

const MODEL_NAMES_GGUF = MODEL_NAMES_AWQ.map((name) => {
  return name.slice(0, -3) + "GGUF";
});

export const MODEL_NAMES = MODEL_NAMES_STD.concat(MODEL_NAMES_GGUF);

//"Xwin-LM/Xwin-LM-13B-V0.2",
//"Xwin-LM/Xwin-LM-7B-V0.2",
//"TheBloke/Xwin-LM-13B-v0.1-AWQ",
//"TheBloke/Xwin-LM-70B-V0.1-AWQ",
//"TheBloke/XwinCoder-34B-AWQ",
//"allenai/tulu-2-dpo-70b",
//"allenai/tulu-2-dpo-13b",
//"NousResearch/Yarn-Mistral-7b-128k",
//"meta-llama/Llama-2-7b-chat-hf",
//"TheBloke/Llama-2-7B-Chat-AWQ",

export const DEFAULT_MODEL_NAME = "TheBloke/OpenHermes-2.5-Mistral-7B-16k-GGUF";
