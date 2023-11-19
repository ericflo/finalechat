"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useCallback, useEffect } from "react";
import MessageList from "./message_list";
import MessageInput from "./message_input";
import {
  useGetMessages,
  useCreateMessage,
  useCreateChat,
} from "../../hooks/api";

export interface ChatWindowProps {
  chatId: number;
  onChatCreated: (chatId: number) => void;
}

const ChatWindow = ({ chatId, onChatCreated }: ChatWindowProps) => {
  const {
    getMessages,
    messages,
    //loading: messagesLoading,
    error: messagesError,
  } = useGetMessages();
  const {
    createMessage,
    //message: createdMessage,
    //loading: messageLoading,
    error: messageError,
  } = useCreateMessage();
  const {
    createChat,
    //chat: createdChat,
    //loading: chatLoading,
    error: chatError,
  } = useCreateChat();

  useEffect(() => {
    if (chatId > 0) {
      getMessages(chatId);
    }
  }, [chatId, getMessages]);

  const handleSendMessage = useCallback(
    (text: string) => {
      (async () => {
        if (chatId < 0) {
          const newChatId = await createChat();
          await createMessage(newChatId, text, "user");
          onChatCreated(newChatId);
          return;
        } else {
          await createMessage(chatId, text, "user");
          getMessages(chatId);
        }
      })();
    },
    [chatId, createChat, createMessage, getMessages, onChatCreated]
  );

  const msgs = (chatId > 0 ? messages?.items : null) || [];

  return (
    <div className="d-flex flex-column h-100">
      {chatError && <div className="alert alert-danger">{chatError}</div>}
      {messagesError && (
        <div className="alert alert-danger">{messagesError}</div>
      )}
      {messageError && <div className="alert alert-danger">{messageError}</div>}
      <MessageList messages={msgs} />
      <MessageInput chatId={chatId} onSendMessage={handleSendMessage} />
    </div>
  );
};

export default ChatWindow;
