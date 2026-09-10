import { createRoot } from 'react-dom/client';
import './i18n';
import App from './App';
import { LoginPage } from './Auth';
import CredentialUpdatePage from './CredentialUpdatePage';
import './styles.css';

const pathname = window.location.pathname;
const isLoginPage = pathname === '/login';
const isCredentialUpdatePage = pathname === '/change-password';

const page = isLoginPage
  ? <LoginPage />
  : isCredentialUpdatePage
    ? <CredentialUpdatePage />
    : <App />;

createRoot(document.getElementById('root')!).render(page);
