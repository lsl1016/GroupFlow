import { request, setToken } from './request';
import type { User } from '../types';
export async function login(username: string): Promise<User> {
  const data = await request<User & { token:string }>('/auth/login', { method:'POST', body: JSON.stringify({ username }) });
  setToken(data.token); return data;
}
export const me = () => request<User>('/auth/me');
