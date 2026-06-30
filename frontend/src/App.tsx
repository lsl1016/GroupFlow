import { useEffect } from 'react';
import { me } from './api/authApi';
import { getToken } from './api/request';
import { useStore } from './stores/useStore';
import { LoginPage } from './pages/LoginPage';
import { ChatPage } from './pages/ChatPage';

export function App() {
  const user = useStore(s => s.user);
  useEffect(() => { if (getToken() && !user) me().then(u => useStore.getState().setUser(u)).catch(() => localStorage.removeItem('groupflow_token')); }, []);
  return user ? <ChatPage/> : <LoginPage/>;
}
