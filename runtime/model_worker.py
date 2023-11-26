import argparse
import asyncio
import datetime
import time
import traceback
import json
from typing import Optional

from vllm.sampling_params import SamplingParams
from vllm.engine.arg_utils import AsyncEngineArgs
from vllm.engine.async_llm_engine import AsyncLLMEngine
from vllm.utils import random_uuid

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
        # max_tokens=data.get("max_tokens", 1024),
        max_tokens=data.get("max_tokens", 64),
        logprobs=data.get("logprobs", None),
        prompt_logprobs=data.get("prompt_logprobs", None),
        skip_special_tokens=data.get("skip_special_tokens", True),
        spaces_between_special_tokens=data.get("spaces_between_special_tokens", True),
    )


def get_llm_kwargs(model, llm_params, dtype="auto", max_model_len=None):
    default_llm_params = {
        "tokenizer_mode": "auto",
        "trust_remote_code": False,
        "tensor_parallel_size": 1,
        "dtype": dtype,
        "seed": 0,
        "gpu_memory_utilization": 0.9,
        "swap_space": 4,
    }
    resp = {"model": model}
    if max_model_len:
        resp["max_model_len"] = max_model_len
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


async def process_chat(chat, llm: AsyncLLMEngine, messages):
    try:
        DEFAULT_CLIENT.update_chat(chat["id"], status="processing")
        sampling_params = get_sampling_params(json.loads(chat["sampling_params"]))
        prompt = get_prompt(model_name=chat["model"], messages=messages)
        running_text = ""
        finished = False
        msg_id = None
        update_awaitable = None
        while not finished:
            request_id = random_uuid()
            results_generator = llm.generate(prompt, sampling_params, request_id)
            async for result in results_generator:
                for output in result.outputs:
                    finished = finished or (
                        output.finish_reason and output.finish_reason != "length"
                    )
                delta_text = "".join([output.text for output in result.outputs])
                if msg_id:
                    if update_awaitable is not None:
                        update_awaitable.cancel()
                    update_awaitable = asyncio.get_running_loop().run_in_executor(
                        None,
                        DEFAULT_CLIENT.update_message,
                        msg_id,
                        running_text + delta_text,
                    )
                else:
                    msg_id = DEFAULT_CLIENT.create_message(
                        chat["id"], running_text + delta_text, "assistant"
                    )["id"]
            if msg_id is not None:
                DEFAULT_CLIENT.update_message(msg_id, running_text + delta_text)
            running_text += delta_text
            prompt += delta_text
    except (KeyboardInterrupt, SystemExit):
        raise
    except Exception as e:
        traceback.print_exception(e)
    finally:
        DEFAULT_CLIENT.update_chat(chat["id"], status="idle")


def main(model: str, dtype: str, max_model_len: Optional[int]):
    print("Starting...")
    engine_args = AsyncEngineArgs(
        **get_llm_kwargs(model, None, dtype=dtype, max_model_len=max_model_len)
    )
    llm = AsyncLLMEngine.from_engine_args(engine_args)
    print("Model loaded.")

    round = 0
    while True:
        round += 1
        print(f"Round {round}")

        try:
            for workitem in DEFAULT_CLIENT.get_next_workitems(model=model):
                chat, messages = workitem["chat"], workitem["messages"]
                asyncio.run(process_chat(chat, llm, messages))
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
    parser.add_argument(
        "--max_model_len",
        help="Specify the maximum model length",
        default=None,
        type=int,
        required=False,
    )
    args = parser.parse_args()
    main(args.model, args.dtype, args.max_model_len)
