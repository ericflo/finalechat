import datetime
import time
import traceback
import json

from vllm import LLM, SamplingParams

from minichat_convo import get_prompt
from client import DEFAULT_CLIENT

LLM_INSTANCE = {"inst": None}


def get_llm(**kwargs):
    inst = LLM_INSTANCE["inst"]
    if inst is not None:
        return inst
    LLM_INSTANCE["inst"] = LLM(**kwargs)
    return LLM_INSTANCE["inst"]


def get_sampling_params(data):
    n = data.get("n", 1)
    best_of = data.get("best_of", 1)
    presence_penalty = data.get("presence_penalty", 0.0)
    frequency_penalty = data.get("frequency_penalty", 0.0)
    repetition_penalty = data.get("repetition_penalty", 1.0)
    temperature = data.get("temperature", 0.2)
    top_p = data.get("top_p", 1.0)
    top_k = data.get("top_k", -1)
    min_p = data.get("min_p", 0.0)
    use_beam_search = data.get("use_beam_search", False)
    length_penalty = data.get("length_penalty", 1.0)
    early_stopping = data.get("early_stopping", False)
    stop = data.get("stop", None)
    stop_token_ids = data.get("stop_token_ids", None)
    ignore_eos = data.get("ignore_eos", False)
    max_tokens = data.get("max_tokens", 1024)
    logprobs = data.get("logprobs", None)
    prompt_logprobs = data.get("prompt_logprobs", None)
    skip_special_tokens = data.get("skip_special_tokens", True)
    spaces_between_special_tokens = data.get("spaces_between_special_tokens", True)
    return SamplingParams(
        n=n,
        best_of=best_of,
        presence_penalty=presence_penalty,
        frequency_penalty=frequency_penalty,
        repetition_penalty=repetition_penalty,
        temperature=temperature,
        top_p=top_p,
        top_k=top_k,
        min_p=min_p,
        use_beam_search=use_beam_search,
        length_penalty=length_penalty,
        early_stopping=early_stopping,
        stop=stop,
        stop_token_ids=stop_token_ids,
        ignore_eos=ignore_eos,
        max_tokens=max_tokens,
        logprobs=logprobs,
        prompt_logprobs=prompt_logprobs,
        skip_special_tokens=skip_special_tokens,
        spaces_between_special_tokens=spaces_between_special_tokens,
    )


def get_llm_kwargs(model, llm_params):
    default_llm_params = {
        "tokenizer_mode": "auto",
        "trust_remote_code": False,
        "tensor_parallel_size": 1,
        "dtype": "auto",
        "seed": 0,
        "gpu_memory_utilization": 0.9,
        "swap_space": 4,
    }
    resp = {"model": model}
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


def get_all_chats():
    all_chats = []
    more_pages = True
    while more_pages:
        chats = DEFAULT_CLIENT.get_chats()
        all_chats.extend(chats["items"])
        more_pages = chats["page"] < chats["pages"]
    all_chats.sort(
        key=lambda c: datetime.datetime.fromisoformat(c["created_timestamp"])
    )
    return all_chats


def get_all_messages(chat_id):
    all_messages = []
    more_pages = True
    while more_pages:
        messages = DEFAULT_CLIENT.get_messages(chat_id)
        all_messages.extend(messages["items"])
        more_pages = messages["page"] < messages["pages"]
    all_messages.sort(key=lambda m: datetime.datetime.fromisoformat(m["timestamp"]))
    return all_messages


def process_chat(chat, messages):
    try:
        DEFAULT_CLIENT.update_chat(chat["id"], status="processing")
        llm = get_llm(
            **get_llm_kwargs(model=chat["model"], llm_params=chat["llm_params"])
        )
        sampling_params = get_sampling_params(json.loads(chat["sampling_params"]))
        prompt = get_prompt(model_name=chat["model"], messages=messages)
        # print(prompt)
        response = llm.generate([prompt], sampling_params=sampling_params)[0]
        # print(response)
        output = "\n".join([o.text for o in response.outputs])
        DEFAULT_CLIENT.create_message(chat["id"], output, "assistant")
    except (KeyboardInterrupt, SystemExit):
        raise
    finally:
        DEFAULT_CLIENT.update_chat(chat["id"], status="idle")


def main():
    round = 0
    while True:
        round += 1
        print(f"Round {round}")
        try:
            for chat in get_all_chats():
                messages = get_all_messages(chat["id"])
                if (
                    messages
                    and len(messages) > 0
                    and messages[-1]["sender_type"] == "user"
                    and chat["status"] == "idle"
                ):
                    process_chat(chat, messages)
        except (KeyboardInterrupt, SystemExit):
            raise
        except Exception as e:
            traceback.print_exception(e)
        time.sleep(1.0)


if __name__ == "__main__":
    main()
