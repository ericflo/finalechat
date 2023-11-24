"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useEffect, useRef } from "react";
import ChatMessage from "./chat_message";
import EmptyState from "./empty_state";
import { Message } from "../../types";

export interface MessageListProps {
  messages: Message[];
  modelName: string;
  onModelChange: (modelName: string) => void;
}

const MessageList = ({
  messages,
  modelName,
  onModelChange,
}: MessageListProps) => {
  const chatHistoryRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (chatHistoryRef.current) {
      chatHistoryRef.current.scrollTop = chatHistoryRef.current.scrollHeight;
    }
  }, [messages]);

  return (
    <div className="flex-grow-1 overflow-auto" ref={chatHistoryRef}>
      <div className="d-flex justify-content-center w-100">
        <div
          className="chat-history"
          style={{ maxWidth: "800px", width: "100%", padding: "0 0 50px 0" }}
        >
          <h6 className="text-muted text-nowrap my-4" style={{ opacity: 0.5 }}>
            Model: {modelName}
          </h6>
          {messages.length > 0 ? (
            messages.map((message, index) => (
              <ChatMessage key={index} message={message} />
            ))
          ) : (
            <EmptyState modelName={modelName} onModelChange={onModelChange} />
          )}
        </div>
      </div>
    </div>
  );
};

export default MessageList;
