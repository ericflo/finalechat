"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useCallback, useEffect, useState } from "react";
import MessageList from "./message_list";
import MessageInput from "./message_input";
import {
  useGetMessages,
  useCreateMessage,
  useCreateChat,
} from "../../hooks/api";
import { DEFAULT_MODEL_NAME } from "../../model_names";

const DEFAULT_POLL_INTERVAL = 1000;

export interface ChatWindowProps {
  chatId: number;
  pollInterval?: number;
  onChatCreated: (chatId: number) => void;
}

const ChatWindow = ({
  chatId,
  pollInterval,
  onChatCreated,
}: ChatWindowProps) => {
  const {
    getMessages,
    messages,
    error: messagesError,
  } = useGetMessages({ sort: true });
  const [modelName, setModelName] = useState<string>(DEFAULT_MODEL_NAME);
  const { createMessage, error: messageError } = useCreateMessage();
  const { createChat, error: chatError } = useCreateChat();
  const [isPolling, _setIsPolling] = useState(true);

  useEffect(() => {
    console.log("modelName: " + modelName);
  }, [modelName]);

  // Set isPolling to false when the window is unfocused
  useEffect(() => {
    const handleVisibilityChange = () => {
      _setIsPolling(!document.hidden);
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, []);

  useEffect(() => {
    if (chatId > 0) {
      getMessages({ chatId });
    }
  }, [chatId, getMessages]);

  useEffect(() => {
    let intervalId;
    if (isPolling && chatId > 0) {
      intervalId = setInterval(
        () => getMessages({ chatId }),
        pollInterval || DEFAULT_POLL_INTERVAL
      );
    }
    return () => clearInterval(intervalId);
  }, [chatId, isPolling, getMessages]);

  const handleSendMessage = useCallback(
    (text: string) => {
      (async () => {
        if (chatId < 0) {
          const newChatId = await createChat(modelName);
          await createMessage(newChatId, text, "user");
          onChatCreated(newChatId);
          return;
        } else {
          await createMessage(chatId, text, "user");
          getMessages({ chatId });
        }
      })();
    },
    [chatId, modelName, createChat, createMessage, getMessages, onChatCreated]
  );

  return (
    <div className="d-flex flex-column h-100">
      {chatError && <div className="alert alert-danger">{chatError}</div>}
      {messagesError && (
        <div className="alert alert-danger">{messagesError}</div>
      )}
      {messageError && <div className="alert alert-danger">{messageError}</div>}
      <MessageList
        messages={chatId > 0 ? messages : []}
        modelName={modelName}
        onModelChange={setModelName}
      />
      <MessageInput chatId={chatId} onSendMessage={handleSendMessage} />
    </div>
  );
};

export default ChatWindow;
