import dataclasses
from enum import auto, Enum
from typing import List, Tuple, Any, Optional


class SeparatorStyle(Enum):
    """Different separator style."""

    ADD_COLON_SINGLE = auto()
    ADD_COLON_TWO = auto()
    NO_COLON_SINGLE = auto()
    BAIZE = auto()
    PHOENIX = auto()
    MINICHAT = auto()


@dataclasses.dataclass
class Conversation:
    """A class that keeps all conversation history."""

    # System prompts
    system: str
    # Two roles
    roles: List[str]
    # All messages
    messages: List[List[str]]
    # Offset of few shot examples
    offset: int
    # Separator
    sep_style: SeparatorStyle
    sep: str
    sep2: str = None
    # Stop criteria (the default one is EOS token)
    stop_str: str = None
    # Stops generation if meeting any token in this list
    stop_token_ids: List[int] = None

    # Used for the state in the gradio servers.
    # TODO(lmzheng): refactor this
    conv_id: Any = None
    skip_next: bool = False
    model_name: str = None

    def get_prompt(self):
        if self.sep_style == SeparatorStyle.ADD_COLON_SINGLE:
            ret = self.system + self.sep
            for role, message in self.messages:
                if message:
                    ret += role + ": " + message + self.sep
                else:
                    ret += role + ": "
            return ret
        elif self.sep_style == SeparatorStyle.ADD_COLON_TWO:
            seps = [self.sep, self.sep2]
            ret = self.system + seps[0]
            for i, (role, message) in enumerate(self.messages):
                if message:
                    ret += role + ": " + message + seps[i % 2]
                else:
                    ret += role + ": "
            return ret
        elif self.sep_style == SeparatorStyle.NO_COLON_SINGLE:
            ret = self.system
            for role, message in self.messages:
                if message:
                    ret += role + message + self.sep
                else:
                    ret += role
            return ret
        elif self.sep_style == SeparatorStyle.BAIZE:
            ret = self.system + "\n"
            for role, message in self.messages:
                if message:
                    ret += role + message + "\n"
                else:
                    ret += role
            return ret
        elif self.sep_style == SeparatorStyle.PHOENIX:
            ret = self.system
            for role, message in self.messages:
                if message:
                    ret += role + ": " + "<s>" + message + "</s>"
                else:
                    ret += role + ": " + "<s>"
            return ret
        elif self.sep_style == SeparatorStyle.MINICHAT:
            ret = self.system
            for role, message in self.messages:
                if message:
                    ret += role + " " + message + "</s>"
                else:
                    ret += role  # No space is needed.
            return ret
        else:
            raise ValueError(f"Invalid style: {self.sep_style}")

    def append_message(self, role, message):
        self.messages.append([role, message])

    def to_gradio_chatbot(self):
        ret = []
        for i, (role, msg) in enumerate(self.messages[self.offset :]):
            if i % 2 == 0:
                ret.append([msg, None])
            else:
                ret[-1][-1] = msg
        return ret

    def to_openai_api_messages(self):
        ret = [{"role": "system", "content": self.system}]

        for i, (_, msg) in enumerate(self.messages[self.offset :]):
            if i % 2 == 0:
                ret.append({"role": "user", "content": msg})
            else:
                if msg is not None:
                    ret.append({"role": "assistant", "content": msg})
        return ret

    def copy(self):
        return Conversation(
            system=self.system,
            roles=self.roles,
            messages=[[x, y] for x, y in self.messages],
            offset=self.offset,
            sep_style=self.sep_style,
            sep=self.sep,
            sep2=self.sep2,
            stop_str=self.stop_str,
            stop_token_ids=self.stop_token_ids,
            conv_id=self.conv_id,
            model_name=self.model_name,
        )

    def dict(self):
        return {
            "system": self.system,
            "roles": self.roles,
            "messages": self.messages,
            "offset": self.offset,
            "conv_id": self.conv_id,
            "model_name": self.model_name,
        }


def get_minichat_prompt(
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    if system_message is None:
        system_message = "‘MiniChat’是一个由‘Beccurio’开发的AI语言模型。下面是人类和MiniChat之间的一段对话。MiniChat的回复应当尽可能详细，并且以Markdown的形式输出。MiniChat应当拒绝参与违背伦理的讨论。</s>"
    conv = Conversation(
        system=system_message,
        roles=("[|User|]", "[|Assistant|]"),
        messages=[],
        offset=0,
        sep_style=SeparatorStyle.MINICHAT,
        sep="</s>",
    )
    # print(f"messages: {messages}")
    if messages:
        for message in messages:
            role = conv.roles[0]
            message_role = message.get("role", message.get("sender_type", None))
            if message_role == "user":
                role = conv.roles[0]
            elif message_role == "assistant":
                role = conv.roles[1]
            conv.append_message(role, message.get("content", message.get("text", None)))
    conv.append_message(conv.roles[1], None)
    # print([conv.get_prompt()])
    return conv.get_prompt()


def get_xwin_prompt(
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    if system_message is None:
        system_message = "A chat between a curious user and an artificial intelligence assistant. The assistant gives helpful, detailed, and polite answers to the user's questions."
    conv = Conversation(
        system=system_message,
        roles=("USER", "ASSISTANT"),
        messages=[],
        offset=0,
        sep_style=SeparatorStyle.ADD_COLON_TWO,
        sep=" ",
        sep2="</s>",
    )
    # print(f"messages: {messages}")
    if messages:
        for message in messages:
            role = conv.roles[0]
            message_role = message.get("role", message.get("sender_type", None))
            if message_role == "user":
                role = conv.roles[0]
            elif message_role == "assistant":
                role = conv.roles[1]
            conv.append_message(role, message.get("content", message.get("text", None)))
    conv.append_message(conv.roles[1], None)
    # print([conv.get_prompt()])
    return conv.get_prompt()


def get_xwin_coder_prompt(
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    if system_message is None:
        system_message = "A chat between a curious user and an artificial intelligence assistant. The assistant gives helpful, detailed, and polite answers to the user's questions."
    prefixes = {
        "system": "<system>: {content}\n",
        "user": "<user>: {content}\n",
        "model": "<AI>: {content}\n",
    }
    msgs = []
    if system_message:
        msgs.append(("system", system_message))
    for message in messages:
        role = "user"
        message_role = message.get("role", message.get("sender_type", None))
        if message_role == "user":
            role = "user"
        elif message_role == "assistant":
            role = "model"
        msgs.append((role, message.get("content", message.get("text", None))))

    prompt = ""
    for role, content in msgs:
        prompt = prompt + prefixes[role].format(content=content)
    prompt = prompt + "<AI>: "
    return prompt


def get_tulu2_prompt(
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    prompt = ""
    if system_message:
        prompt += f"<|assistant|>\n{system_message}\n"
    if messages:
        for message in messages:
            message_role = message.get("role", message.get("sender_type", None))

            if message_role == "user":
                prompt += f"<|user|>\n"
            elif message_role == "assistant":
                prompt += f"<|assistant|>\n"

            content = message.get("content", message.get("text", None))
            if content:
                prompt += content + "\n"
    prompt += "<|assistant|>\n"
    return prompt


def get_llama2_prompt(
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    def prompt_turn(user_msg, assistant_msg=None):
        resp = f"<s>[INST] {user_msg} [/INST] "
        if assistant_msg:
            resp += f"{assistant_msg} </s>"
        return resp

    if system_message is None:
        system_message = (
            "You are a helpful, respectful, and honest assistant. Always answer as helpfully as possible, "
            "while being safe. Your answers should not include any harmful, unethical, racist, sexist, toxic, "
            "dangerous, or illegal content. Please ensure that your responses are socially unbiased and positive in nature.\n\n"
            "If a question does not make any sense, or is not factually coherent, explain why instead of answering "
            "something not correct. If you don't know the answer to a question, please don't share false information."
        )

    wrapped_system_message = f"""<<SYS>>
{system_message}
<</SYS>>

"""

    prompt = ""
    if messages:
        pair = ["", ""]
        for message in messages:
            message_role = message.get("role", message.get("sender_type", None))
            content = message.get("content", message.get("text", None))
            if message_role == "user":
                pair[0] = content
            elif message_role == "assistant":
                pair[1] = content
            if pair[1]:
                if not prompt:
                    prompt += prompt_turn(wrapped_system_message + pair[0], pair[1])
                else:
                    prompt += prompt_turn(pair[0], pair[1])
                pair[0] = ""
                pair[1] = ""
        if pair[0] or pair[1]:
            prompt += prompt_turn(pair[0], pair[1])
    # print(prompt)
    return prompt


def get_chatml_prompt(
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    """<|im_start|>system
    {system_message}<|im_end|>
    <|im_start|>user
    {prompt}<|im_end|>
    <|im_start|>assistant"""
    prompt = ""
    if system_message:
        prompt += f"<|im_start|>system\n{system_message}<|im_end|>\n"
    if messages:
        for message in messages:
            message_role = message.get("role", message.get("sender_type", None))

            if message_role == "user":
                prompt += f"<|im_start|>user\n"
            elif message_role == "assistant":
                prompt += f"<|im_start|>assistant\n"

            content = message.get("content", message.get("text", None))
            if content:
                prompt += content + "\n"
    prompt += "<|im_start|>assistant"
    return prompt


def get_prompt(
    model_name: str,
    messages: Optional[List[Any]] = None,
    system_message: Optional[str] = None,
) -> str:
    model = model_name.lower()
    if model_name.startswith("GeneZC/MiniChat"):
        return get_minichat_prompt(messages=messages, system_message=system_message)
    elif "openhermes" in model:
        return get_chatml_prompt(messages=messages, system_message=system_message)
    elif "xwincoder" in model:
        return get_xwin_coder_prompt(messages=messages, system_message=system_message)
    elif "xwin-lm" in model:
        return get_xwin_prompt(messages=messages, system_message=system_message)
    elif "tulu-2" in model:
        return get_tulu2_prompt(messages=messages, system_message=system_message)
    elif "yarn-mistral" in model:
        return get_llama2_prompt(messages=messages, system_message=system_message)
    elif "llama-2" in model:
        return get_llama2_prompt(messages=messages, system_message=system_message)
    elif "orca-2" in model:
        return get_chatml_prompt(messages=messages, system_message=system_message)
    elif "codellama" in model:
        return get_llama2_prompt(messages=messages, system_message=system_message)
    else:
        raise ValueError(f"Invalid model: {model_name}")
