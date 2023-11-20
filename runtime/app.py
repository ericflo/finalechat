import json
from threading import local
from flask import Flask, request, jsonify, abort
from minichat_convo import get_minichat_prompt
from vllm import LLM, SamplingParams


app = Flask(__name__)

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
    temperature = data.get("temperature", 1.0)
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


# API Endpoints
@app.route("/completion", methods=["POST"])
def get_completion():
    data = request.get_json()
    sampling_params = get_sampling_params(data)

    model = data.get("model", "codellama/CodeLlama-7b-Instruct-hf")
    if not model:
        abort(400, description="Missing 'model' in request data.")
    messages = data.get("messages", [])
    if not messages:
        abort(400, description="Missing 'messages' in request data.")

    prompt = get_minichat_prompt(messages=messages)
    print(prompt)

    llm = get_llm(model=model)
    response = llm.generate([prompt], sampling_params)[0]
    print(response)
    output = "\n".join([o.text for o in response.outputs])
    print(output)
    return output, 200


# Error handling
@app.errorhandler(400)
def bad_request(error):
    return jsonify({"error": "Bad request", "message": error.description}), 400


@app.errorhandler(404)
def not_found(error):
    return jsonify({"error": "Not found"}), 404


@app.route("/<path:path>", methods=["OPTIONS"])
def handle_options(path):
    response = app.make_default_options_response()
    response.headers.add("Access-Control-Allow-Origin", "*")
    response.headers.add(
        "Access-Control-Allow-Headers",
        "Content-Type,Authorization,Origin,X-Requested-With,Accept,Accept-Language,Content-Language",
    )
    response.headers.add("Access-Control-Allow-Methods", "GET,PUT,POST,DELETE,OPTIONS")
    return response


# This is how we allow all origins to access our API
# This is not secure and should not be used in production
@app.after_request
def after_request(response):
    response.headers.add("Access-Control-Allow-Origin", "*")
    response.headers.add(
        "Access-Control-Allow-Headers",
        "Content-Type,Authorization,Origin,X-Requested-With,Accept,Accept-Language,Content-Language",
    )
    response.headers.add("Access-Control-Allow-Methods", "GET,PUT,POST,DELETE,OPTIONS")
    return response


if __name__ == "__main__":
    app.run(
        host="0.0.0.0", port=7025, debug=False
    )  # Turn off debug mode for production
