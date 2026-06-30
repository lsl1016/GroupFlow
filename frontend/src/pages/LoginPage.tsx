import { useState } from 'react';
import { login } from '../api/authApi';
import { useStore } from '../stores/useStore';
import { wsClient } from '../websocket/wsClient';

export function LoginPage() {
  const [username, setUsername] = useState('user_001');
  const [error, setError] = useState('');
  async function submit() {
    try { const u = await login(username); useStore.getState().setUser(u); wsClient.connect(); } catch (e:any) { setError(e.message); }
  }
  return <div className="login-page"><div className="login-card"><h1>GroupFlow 群流</h1><p>一期 MVP：大群优先的实时群聊系统</p><input value={username} onChange={e=>setUsername(e.target.value)} placeholder="user_001"/><button onClick={submit}>登录</button>{error && <em>{error}</em>}<small>可用测试用户：user_001 / user_002 / user_003 / user_004 / user_005</small></div></div>;
}
