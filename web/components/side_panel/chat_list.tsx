"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { Chat } from "../../types";
import ChatListItem from "./chat_list_item";

interface ChatListProps {
  chats: Chat[];
  isLoading: boolean;
  onChatSelected: (chatId: number) => void;
}

const ChatList = ({ chats, isLoading, onChatSelected }: ChatListProps) => {
  return (
    <div className="flex-grow-1 overflow-auto m-2">
      <ul className="list-group">
        {chats.map((chat) => (
          <ChatListItem
            key={chat.id}
            chat={chat}
            onSelect={() => onChatSelected(chat.id)}
          />
        ))}
      </ul>
      {/*isLoading && <div>Loading more chats...</div>*/}
    </div>
  );
};

export default ChatList;
