export interface User {
  id: number;
  email: string;
  username: string;
  created_timestamp: string;
}

export interface Chat {
  id: number;
  user_id: number;
  summary: string;
  model: string;
  status: string;
  llm_params: string;
  sampling_params: string;
  created_timestamp: string;
  //messages?: Message[];
}

export interface Message {
  id: number;
  user_id: number;
  chat_id: number;
  text: string;
  sender_type: string;
  timestamp: string;
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  pages: number;
  page: number;
}
