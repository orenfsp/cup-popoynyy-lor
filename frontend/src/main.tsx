import React, {useEffect,useState} from 'react'
import {createRoot} from 'react-dom/client'
import {BrowserRouter,Link,Navigate,Route,Routes,useNavigate} from 'react-router-dom'
import './styles.css'
import './staff.css'
import './enhancements.css'
import './acceptance.css'
import StaffWorkspace from './StaffWorkspace'
import ApplicantActions from './ApplicantActions'
import NewAppeal from './NewAppeal'
import Appeal from './Appeal'

const API='/api/v1'
const categories=[['bullying','Травля и оскорбления'],['classmate_conflict','Конфликт с одноклассниками'],['cyberbullying','Кибербуллинг'],['threats','Давление и угрозы'],['teacher_conflict','Конфликт с учителем'],['parent_conflict','Конфликт с родителями'],['legal','Вопрос юридического характера'],['unknown','Не знаю, как это назвать']]
type Created={publicId:string;accessCode:string;accessFragment:string;crisisHelpRequired:boolean}

function App(){if((import.meta as any).env.VITE_STAFF_SITE==='true')return <StaffWorkspace/>;return <Routes><Route path="/" element={<Home/>}/><Route path="/new" element={<NewAppeal Shell={Shell} Crisis={Crisis}/>}/><Route path="/access" element={<Access/>}/><Route path="/appeal" element={<Appeal Shell={Shell} Crisis={Crisis}/>}/><Route path="*" element={<Navigate to="/"/>}/></Routes>}
function Shell({children}:{children:React.ReactNode}){return <><header><Link to="/" className="brand">молва</Link></header><main>{children}</main><footer>Без имени и регистрации · аналитика содержит только обезличенные показатели</footer></>}
function Home(){return <Shell><section className="hero"><span className="eyebrow">ПЛАТФОРМА ДОВЕРИТЕЛЬНЫХ ОБРАЩЕНИЙ</span><h1>О сложном можно рассказать</h1><p>Здесь тебя выслушают без имени и регистрации. Обращение увидит оператор, а затем подходящий специалист.</p><div className="actions"><Link className="primary" to="/new">Рассказать о ситуации</Link><Link className="secondary" to="/access">Проверить обращение</Link></div><div className="trust"><b>Почему это безопасно</b><p>Мы не просим имя или телефон. Для возвращения ты получишь единый секретный код. Email можно добавить только по желанию.</p></div></section></Shell>}
function Access(){const [accessCode,setAccessCode]=useState('');const [error,setError]=useState('');const nav=useNavigate();useEffect(()=>{const raw=decodeURIComponent(location.hash.slice(1));if(raw){setAccessCode(raw);history.replaceState(null,'',location.pathname)}},[]);async function login(){const res=await fetch(`${API}/appeal-sessions`,{method:'POST',credentials:'include',headers:{'Content-Type':'application/json'},body:JSON.stringify({accessCode})});if(res.ok)nav('/appeal');else setError((await res.json()).error?.message)}return <Shell><section className="form"><span className="eyebrow">ВОЗВРАЩЕНИЕ К ОБРАЩЕНИЮ</span><h1>Введи код доступа</h1><label>Единый код обращения<input value={accessCode} onChange={e=>setAccessCode(e.target.value.toUpperCase())} placeholder="МОЛ-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX"/></label>{error&&<p className="error">{error}</p>}<button className="primary" onClick={login}>Открыть</button></section></Shell>}
function Crisis(){return <aside className="crisis" role="alert"><b>Если сейчас тяжело или небезопасно</b><p>Можно продолжить рассказ здесь. Единый общероссийский телефон доверия для детей, подростков и родителей работает бесплатно и анонимно:</p><div className="crisis-phones"><a href="tel:88002000122">8 (800) 2000-122</a><span>или</span><a href="tel:124">124 с мобильного</a></div><small>При непосредственной угрозе жизни позвоните 112.</small></aside>}
function statusText(v:string){return ({NEW:'Мы получили обращение',ASSIGNED:'Передали специалисту',IN_PROGRESS:'Специалист разбирается',WAITING_FOR_APPLICANT:'Нужен ваш ответ',ANSWER_READY:'Рекомендации готовы',RETURNED:'Мы вернулись к ситуации',COMPLETED:'Обращение завершено'} as Record<string,string>)[v]||v}
createRoot(document.getElementById('root')!).render(<React.StrictMode><BrowserRouter><App/></BrowserRouter></React.StrictMode>)
// The prototype used to cache the whole applicant shell. That made a stopped
// local service look alive and could leave an outdated privacy-sensitive UI in
// the browser. Remove previous registrations and their caches instead.
if('serviceWorker' in navigator&&(import.meta as any).env.VITE_STAFF_SITE!=='true')window.addEventListener('load',async()=>{
  try{
    const registrations=await navigator.serviceWorker.getRegistrations()
    await Promise.all(registrations.map(registration=>registration.unregister()))
    if('caches' in window){
      const names=await caches.keys()
      await Promise.all(names.filter(name=>name.startsWith('molva-')).map(name=>caches.delete(name)))
    }
  }catch{}
})
