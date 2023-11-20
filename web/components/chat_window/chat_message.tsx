"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { Message } from "../../types";
import Markdown from "react-markdown";

export interface ChatMessageProps {
  message: Message;
}

const ChatMessage = ({ message }: ChatMessageProps) => (
  <div className="message py-2 my-1">
    <strong>{message.sender_type}</strong>
    <br />
    <Markdown>{message.text}</Markdown>
  </div>
);

export default ChatMessage;
