import type { ApiResp } from '../types';

const API_BASE = '/api/v1';
export function getToken() { return localStorage.getItem('groupflow_token') || ''; }
export function setToken(token: string) { localStorage.setItem('groupflow_token', token); }
export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json', ...(init.headers as any || {}) };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(`${API_BASE}${path}`, { ...init, headers });
  const body = await res.json() as ApiResp<T>;
  if (!res.ok || body.errNo !== 0) throw new Error(body.errMsg || `errNo=${body.errNo}`);
  return body.data;
}
