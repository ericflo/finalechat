import argparse
import datetime
import time
import traceback
import json

from vllm import LLM, SamplingParams

from minichat_convo import get_prompt
from client import DEFAULT_CLIENT


def get_sampling_params(data):
    return SamplingParams(
        n=data.get("n", 1),
        best_of=data.get("best_of", 1),
        presence_penalty=data.get("presence_penalty", 0.0),
        frequency_penalty=data.get("frequency_penalty", 0.0),
        repetition_penalty=data.get("repetition_penalty", 1.0),
        temperature=data.get("temperature", 0.2),
        top_p=data.get("top_p", 1.0),
        top_k=data.get("top_k", -1),
        min_p=data.get("min_p", 0.0),
        use_beam_search=data.get("use_beam_search", False),
        length_penalty=data.get("length_penalty", 1.0),
        early_stopping=data.get("early_stopping", False),
        stop=data.get("stop", None),
        stop_token_ids=data.get("stop_token_ids", None),
        ignore_eos=data.get("ignore_eos", False),
        max_tokens=data.get("max_tokens", 1024),
        logprobs=data.get("logprobs", None),
        prompt_logprobs=data.get("prompt_logprobs", None),
        skip_special_tokens=data.get("skip_special_tokens", True),
        spaces_between_special_tokens=data.get("spaces_between_special_tokens", True),
    )


def get_llm_kwargs(model, llm_params, dtype="auto"):
    default_llm_params = {
        "tokenizer_mode": "auto",
        "trust_remote_code": False,
        "tensor_parallel_size": 1,
        "dtype": dtype,
        "seed": 0,
        "gpu_memory_utilization": 0.9,
        "swap_space": 4,
        "max_model_len": 4096,
    }
    resp = {"model": model, "max_model_len": default_llm_params["max_model_len"]}
    data = json.loads(llm_params) if llm_params else {}
    if "tokenizer" in data:
        resp["tokenizer"] = data["tokenizer"]
    resp["tokenizer_mode"] = data.get(
        "tokenizer_mode", default_llm_params["tokenizer_mode"]
    )
    resp["trust_remote_code"] = data.get(
        "trust_remote_code", default_llm_params["trust_remote_code"]
    )
    resp["tensor_parallel_size"] = data.get(
        "tensor_parallel_size", default_llm_params["tensor_parallel_size"]
    )
    resp["dtype"] = data.get("dtype", default_llm_params["dtype"])
    if "quantization" in data:
        resp["quantization"] = data["quantization"]
    if "revision" in data:
        resp["revision"] = data["revision"]
    if "tokenizer_revision" in data:
        resp["tokenizer_revision"] = data["tokenizer_revision"]
    resp["seed"] = data.get("seed", default_llm_params["seed"])
    resp["gpu_memory_utilization"] = data.get(
        "gpu_memory_utilization", default_llm_params["gpu_memory_utilization"]
    )
    resp["swap_space"] = data.get("swap_space", default_llm_params["swap_space"])
    return resp


def process_chat(chat, llm, messages):
    try:
        DEFAULT_CLIENT.update_chat(chat["id"], status="processing")
        prompt = get_prompt(model_name=chat["model"], messages=messages)
        sampling_params = get_sampling_params(json.loads(chat["sampling_params"]))
        response = llm.generate([prompt], sampling_params=sampling_params)[0]
        output = "\n".join([o.text for o in response.outputs])
        DEFAULT_CLIENT.create_message(chat["id"], output, "assistant")
    except (KeyboardInterrupt, SystemExit):
        raise
    except Exception as e:
        traceback.print_exception(e)
    finally:
        DEFAULT_CLIENT.update_chat(chat["id"], status="idle")


def main(model: str, dtype: str):
    print("Starting...")
    llm = LLM(
        **get_llm_kwargs(model, None, dtype=dtype)
    )  # TODO: How to handle `llm_params`?
    print("Model loaded.")

    round = 0
    while True:
        round += 1
        print(f"Round {round}")

        try:
            for workitem in DEFAULT_CLIENT.get_next_workitems(model=model):
                chat, messages = workitem["chat"], workitem["messages"]
                process_chat(chat, llm, messages)
        except (KeyboardInterrupt, SystemExit):
            print("Exiting...")
            break
        except Exception as e:
            traceback.print_exception(e)

        time.sleep(1.0)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Worker for evaluating responses to chats for a single model"
    )
    parser.add_argument(
        "--model",
        help="Specify the model name or path",
        default="TheBloke/OpenHermes-2.5-Mistral-7B-16k-AWQ",
    )
    parser.add_argument(
        "--dtype",
        help="Specify the data type",
        default="auto",
        choices=["auto", "float32", "float16", "bfloat16"],
    )
    args = parser.parse_args()
    main(args.model, args.dtype)
