import React,{useState} from 'react'

const active=['ASSIGNED','IN_PROGRESS','WAITING_FOR_APPLICANT','RETURNED']

export default function ApplicantActions({status,returnCount=0,feedback,adult=false,reload}:{status:string;returnCount?:number;feedback?:any;adult?:boolean;reload:()=>void}){
 const [error,setError]=useState(''),[busy,setBusy]=useState(false),[reason,setReason]=useState(''),[rating,setRating]=useState(feedback?.rating||0),[comment,setComment]=useState(feedback?.comment||''),[complaint,setComplaint]=useState('')
 async function request(path:string,body:any){setBusy(true);setError('');const r=await fetch('/api/v1/my-appeal/'+path,{method:'POST',credentials:'include',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});if(r.ok)await reload();else setError((await r.json()).error?.message||'Не получилось выполнить действие');setBusy(false)}
 async function change(next:string){return request('status',{status:next})}
 async function resolve(helpful:boolean){return request('feedback',{helpful,reason,rating,comment,complaint:false,complaintText:''})}
 async function saveReview(){return request('feedback',{helpful:feedback?.helpful??true,reason:'',rating,comment,complaint:false,complaintText:''})}
 async function sendComplaint(){return request('feedback',{helpful:feedback?.helpful??false,reason:reason||'Жалоба на специалиста',rating,comment,complaint:true,complaintText:complaint})}
 return <section className="conversation-actions">
  {status==='ANSWER_READY'&&<div className="answer-decision"><h3>Помогли ли рекомендации?</h3><p>Можно завершить обращение или вернуть его оператору, чтобы подобрать другой вариант помощи.</p><div className="form-actions"><button className="primary" disabled={busy} onClick={()=>resolve(true)}>Это помогло</button><button className="secondary" disabled={busy||returnCount>=2||reason.trim().length<3} onClick={()=>resolve(false)}>Это не помогло</button></div>{returnCount<2&&<textarea value={reason} onChange={e=>setReason(e.target.value)} placeholder={adult?'Если ответ не помог, расскажите, чего не хватило':'Если ответ не помог, расскажи, чего не хватило'}/>} {returnCount>=2&&<p className="notice">Обращение уже возвращалось дважды. Можно пожаловаться оператору или завершить общение.</p>}</div>}
  {status==='COMPLETED'?<div className="conversation-ended"><div><b>Общение завершено</b><p>Чат и код доступа сохранены. Если снова понадобится помощь, разговор можно продолжить.</p></div><button className="primary" disabled={busy} onClick={()=>change('RETURNED')}>Возобновить общение</button></div>:active.includes(status)?<button className="secondary finish-conversation" disabled={busy} onClick={()=>change('COMPLETED')}>Завершить общение</button>:null}
  {status==='COMPLETED'&&<div className="feedback-card"><h3>Оценить помощь</h3><div className="rating" aria-label="Оценка от 1 до 5">{[1,2,3,4,5].map(n=><button type="button" aria-label={`${n} из 5`} className={rating>=n?'selected':''} onClick={()=>setRating(n)} key={n}>★</button>)}</div><textarea value={comment} onChange={e=>setComment(e.target.value)} placeholder="Комментарий — необязательно"/><button className="secondary" disabled={busy||rating<1} onClick={saveReview}>Сохранить оценку</button></div>}
  <details className="complaint"><summary>Пожаловаться на специалиста</summary><p>Жалобу получит оператор, эксперт её не увидит.</p><textarea value={complaint} onChange={e=>setComplaint(e.target.value)} placeholder={adult?'Расскажите, что произошло':'Расскажи, что произошло'}/><button className="secondary" disabled={busy||complaint.trim().length<3} onClick={sendComplaint}>Отправить оператору</button></details>
  {error&&<p className="error">{error}</p>}
 </section>
}
