"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useEffect, useCallback } from "react";
import SearchBar from "./search_bar";
import ChatList from "./chat_list";
import UserProfileSection from "./user_profile_section";
import { useGetChats, useDeleteChat } from "../../hooks/api";
import { User } from "../../types";

export interface SidePanelProps {
  token: string;
  user: User;
  setToken: (token: string) => void;
  chatId: number;
  onChatSelected: (chatId: number) => void;
}

export default function SidePanel({
  token,
  user,
  setToken,
  chatId,
  onChatSelected,
}: SidePanelProps) {
  const { getChats, chats, loading, error } = useGetChats(token);
  const { deleteChat, error: deleteChatError } = useDeleteChat(token);
  useEffect(() => {
    if (token) {
      getChats({ reverse: true });
    }
  }, [token, chatId, getChats]);
  const handleNewChat = useCallback(() => {
    console.log("abcdefgkljkkljlkj");
    onChatSelected(-1);
  }, [onChatSelected]);
  const handleLogout = useCallback(() => setToken(""), [setToken]);
  const handleChatDeleteRequest = useCallback(
    (chatId: number) => {
      if (confirm("Are you sure you want to delete this chat?")) {
        deleteChat(chatId);
        getChats({ reverse: true });
      }
    },
    [deleteChat]
  );
  return (
    <div className="d-flex flex-column h-100">
      {error && <div className="alert alert-danger">{error}</div>}
      {deleteChatError && (
        <div className="alert alert-danger">{deleteChatError}</div>
      )}
      <SearchBar onNewChat={handleNewChat} />
      <div className="flex-grow-1 overflow-auto">
        <ChatList
          chats={chats}
          isLoading={loading}
          onChatSelected={onChatSelected}
          onChatDeleteRequest={handleChatDeleteRequest}
        />
      </div>
      <div className="mt-auto">
        <UserProfileSection user={user} onLogout={handleLogout} />
      </div>
    </div>
  );
}
