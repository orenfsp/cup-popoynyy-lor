"""Integration checks against local Compose. Creates isolated QA users and appeals."""
import atexit, base64, json, os, uuid, urllib.request, urllib.error, http.cookiejar

PASSWORD = os.environ.get('STAFF_BOOTSTRAP_PASSWORD', 'Molva-local-2026!')
class Client:
    def __init__(self, port=8089):
        self.base=f'http://localhost:{port}/api/v1'
        self.http=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    def call(self,path,data=None,method=None,expected=200):
        req=urllib.request.Request(self.base+path,data=json.dumps(data).encode() if data is not None else None,method=method or ('POST' if data is not None else 'GET'),headers={'Content-Type':'application/json','X-Molva-Request':'1'})
        try:
            response=self.http.open(req)
        except urllib.error.HTTPError as e:
            response=e
        raw=response.read().decode()
        assert response.status==expected,(path,response.status,expected,raw)
        return json.loads(raw) if raw and raw[0] in '{[' else raw
    def login(self,email,password=PASSWORD):
        return self.call('/staff/login',{'email':email,'password':password})
    def multipart(self,path,payload,files,expected=201):
        boundary='----molva-'+uuid.uuid4().hex
        parts=[]
        def add(value): parts.append(value if isinstance(value,bytes) else value.encode())
        add(f'--{boundary}\r\nContent-Disposition: form-data; name="payload"\r\n\r\n{json.dumps(payload)}\r\n')
        for name,content_type,data in files:
            add(f'--{boundary}\r\nContent-Disposition: form-data; name="files"; filename="{name}"\r\nContent-Type: {content_type}\r\n\r\n');add(data);add('\r\n')
        add(f'--{boundary}--\r\n')
        req=urllib.request.Request(self.base+path,data=b''.join(parts),method='POST',headers={'Content-Type':f'multipart/form-data; boundary={boundary}','X-Molva-Request':'1'})
        try:
            response=self.http.open(req)
        except urllib.error.HTTPError as e:
            response=e
        raw=response.read().decode();assert response.status==expected,(response.status,raw);return json.loads(raw) if raw else raw

admin,operator,expert,other,applicant,anonymous=[Client(),Client(),Client(),Client(),Client(8088),Client()]
admin.login('admin@molva.local');operator.login('operator@molva.local');expert.login('expert@molva.local')
cleanup_codes=[]
cleanup_questions=[]
cleanup_categories=[]
cleanup_groups=[]
cleanup_staff=[]
def cleanup_qa_data():
    # Keep failed test runs out of the public questionnaire and staff queues.
    for access_code in cleanup_codes:
        try:
            client=Client(8088)
            client.call('/appeal-sessions',{'accessCode':access_code},expected=201)
            client.call('/my-appeal',method='DELETE',expected=204)
        except Exception:
            pass
    for question in reversed(cleanup_questions):
        try:
            admin.call('/admin/questions/'+question['id'],{**question,'active':False},method='PUT',expected=204)
        except Exception:
            pass
    for category in cleanup_categories:
        try:
            admin.call('/admin/categories',{**category,'active':False,'expertId':'','groupId':''},expected=204)
        except Exception:
            pass
    for group in cleanup_groups:
        try:
            admin.call('/admin/groups',{
                'id':group['id'],'name':group['name'],
                'maxActive':group['max_active_per_expert'],'active':False,
                'expertIds':group['expert_ids'],
            },expected=204)
        except Exception:
            pass
    for user,account in cleanup_staff:
        try:
            admin.call('/admin/staff/'+user['id'],{**account,'active':False,'password':''},method='PUT',expected=204)
        except Exception:
            pass
atexit.register(cleanup_qa_data)
def questionnaire_answers(extra=None,skip=()):
    result=dict(extra or {})
    questions=applicant.call('/questionnaire')['items']
    for _ in questions:
        for q in questions:
            parent=q.get('showIfQuestionCode')
            parent_value=result.get(parent)
            visible=not parent or (any(v in q.get('showIfValues',[]) for v in parent_value) if isinstance(parent_value,list) else parent_value in q.get('showIfValues',[]))
            if visible and q['required'] and q['code'] not in result and q['code'] not in skip:
                result[q['code']]='Тестовый ответ' if q['answerType']=='TEXT' else ([q['options'][0]] if q['answerType']=='MULTIPLE' else q['options'][0])
    return result
suffix=uuid.uuid4().hex[:10]
account={'email':f'qa-{suffix}@molva.local','displayName':'Тестовый эксперт','role':'EXPERT','password':'QA-expert-password-2026'}
user=admin.call('/admin/staff',account,expected=201)
cleanup_staff.append((user,account))
other.login(account['email'],account['password'])
anonymous.call('/staff/cases',expected=401)
applicant.call('/analytics',expected=403)
anonymous.call('/analytics',expected=401)
operator.call('/analytics',expected=403)
operator.call('/admin/staff',expected=403)

applicant.call('/appeals',{'applicantType':'STUDENT','categoryCode':'unknown','body':'Проверка неверной почты '+suffix,'answers':questionnaire_answers(),'email':'not-an-email'},expected=422)
png=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=')+b'EXIF_GPS_TEST'
upload=applicant.multipart('/appeals',{'applicantType':'STUDENT','categoryCode':'unknown','body':'Проверка безопасного вложения '+suffix,'answers':questionnaire_answers(),'email':''},[('screen.png','image/png',png)])
cleanup_codes.append(upload['accessCode'])
upload_client=Client(8088);upload_client.call('/appeal-sessions',{'accessCode':upload['accessCode']},expected=201)
uploaded=upload_client.call('/my-appeal')['appeal']['attachments'];assert len(uploaded)==1
attachment=upload_client.http.open(upload_client.base+'/my-appeal/attachments/'+uploaded[0]['id']).read()
assert attachment.startswith(b'\x89PNG') and b'EXIF_GPS_TEST' not in attachment
upload_client.call('/my-appeal',method='DELETE',expected=204)
created=applicant.call('/appeals',{'applicantType':'STUDENT','categoryCode':'unknown','body':'Проверка матрицы доступа '+suffix,'answers':questionnaire_answers({'where_happens':'В школе'}),'email':f'applicant-{suffix}@example.test'},expected=201)
cleanup_codes.append(created['accessCode'])
case_id=created['appeal']['id']
applicant.call('/appeal-sessions',{'accessCode':created['accessCode']},expected=201)
path='/staff/cases/'+case_id
def action(client,action,expected=204,**kwargs):
    return client.call(path+'/actions',{'action':action,**kwargs},expected=expected)

assert 'body' not in admin.call(path)
operator_detail=operator.call(path)
assert 'body' in operator_detail and operator_detail['canChat'] is True
assert operator_detail['answerItems'] and operator_detail['answerItems'][0]['question']!='where_happens'
other.call(path,expected=403)
operator.multipart(path+'/messages',{'body':'Уточните, пожалуйста, обстоятельства'},[('operator.png','image/png',png)],expected=201)
assert operator.call(path)['messages'][0]['attachments'][0]['file_name']=='operator.png'
action(operator,'assign',expertId='00000000-0000-4000-8000-000000000002')
operator.call(path+'/messages',{'body':'После назначения оператор писать не может'},expected=403)
action(expert,'priority',expected=403,value='URGENT')
action(admin,'reply',expected=403,body='Запрещённый ответ')
action(operator,'note',expected=403,body='Запрещённая заметка')
action(expert,'reply',body='Ответ заявителю')
expert.multipart(path+'/messages',{'body':'Ответ со скриншотом'},[('expert.png','image/png',png)],expected=201)
action(expert,'note',body='Приватная заметка')
expert_messages=expert.call(path)['messages']
assert any(m['body']=='Ответ заявителю' for m in expert_messages)
assert any(m['attachments'] for m in expert_messages)
assert len(expert.call(path)['notes'])==1
assert 'notes' not in operator.call(path)
assert 'messages' not in admin.call(path)
applicant.multipart('/my-appeal/messages',{'body':'Мне хочется умереть'},[('chat.png','image/png',png)],expected=201)
own=applicant.call('/my-appeal')
assert any(m['authorName']=='Психолог Алексей' for m in own['messages'])
assert own['appeal']['crisisFlag'] is True
assert any(m['attachments'] for m in own['messages'])
assert 'notes' not in own
action(expert,'request',value='COEXECUTOR',body='Нужна помощь коллеги')
request=operator.call(path)['requests'][0]
action(operator,'coexecutor',expertId=user['id'],requestId=request['id'])
assert any(m['body']=='Ответ со скриншотом' for m in other.call(path)['messages'])
action(expert,'request',value='TRANSFER',body='Прошу передать коллеге')
request=operator.call(path)['requests'][0]
action(admin,'assign',expertId=user['id'],requestId=request['id'],body='Административное подтверждение передачи')
expert.call(path,expected=403)
action(expert,'reply',expected=403,body='Доступ уже отозван')
action(other,'status',value='ANSWER_READY')
action(other,'status',value='COMPLETED',expected=403)
action(admin,'status',value='COMPLETED',expected=403)
applicant.call('/my-appeal/status',{'status':'COMPLETED'},expected=204)
applicant.call('/my-appeal/messages',{'body':'В завершённый чат писать нельзя'},expected=403)
applicant.call('/my-appeal/status',{'status':'RETURNED'},expected=204)
applicant.call('/my-appeal/messages',{'body':'После возобновления снова можно писать'},expected=201)
action(other,'status',value='IN_PROGRESS')
action(other,'status',value='ANSWER_READY')
action(operator,'status',value='COMPLETED')
action(admin,'status',value='RETURNED',expected=403)
action(operator,'status',value='RETURNED')
decision_client=Client(8088)
decision=decision_client.call('/appeals',{'applicantType':'STUDENT','categoryCode':'unknown','body':'Проверка ветки не помогло '+suffix,'answers':questionnaire_answers()},expected=201)
cleanup_codes.append(decision['accessCode'])
decision_id=decision['appeal']['id'];decision_path='/staff/cases/'+decision_id
decision_client.call('/appeal-sessions',{'accessCode':decision['accessCode']},expected=201)
operator.call(decision_path+'/actions',{'action':'assign','expertId':'00000000-0000-4000-8000-000000000002'},expected=204)
expert.call(decision_path+'/actions',{'action':'status','value':'ANSWER_READY'},expected=204)
decision_client.call('/my-appeal/feedback',{'helpful':False,'reason':'Нужен другой взгляд','rating':0,'comment':'','complaint':False,'complaintText':''},expected=204)
assert decision_client.call('/my-appeal')['appeal']['status']=='RETURNED'
expert.call(decision_path,expected=403)
assert operator.call(decision_path)['feedback']['helpful'] is False
operator.call(decision_path+'/actions',{'action':'assign','expertId':'00000000-0000-4000-8000-000000000002'},expected=204)
expert.call(decision_path+'/actions',{'action':'status','value':'ANSWER_READY'},expected=204)
decision_client.call('/my-appeal/feedback',{'helpful':True,'reason':'','rating':5,'comment':'Спасибо','complaint':False,'complaintText':''},expected=204)
assert decision_client.call('/my-appeal')['appeal']['status']=='COMPLETED'
for client in [admin,operator,other]:
    client.call('/staff/statistics')
    assert 'Дата' in client.call('/staff/statistics?format=csv')
rows=other.call('/staff/statistics')['items']
assert all(r['action'] in ['status'] for r in rows),rows
applicant.call('/my-appeal',method='DELETE',expected=204)
assert other.call('/staff/statistics')['items']
other.call(path,expected=404)
admin.call('/admin/staff/'+user['id'],{**account,'active':False,'password':''},method='PUT',expected=204)
other.call('/staff/me',expected=401)
admin.call('/admin/groups',{'name':'QA группа '+suffix,'maxActive':20,'active':True,'expertIds':['00000000-0000-4000-8000-000000000002']},expected=204)
group=next(v for v in admin.call('/admin/groups')['items'] if v['name']=='QA группа '+suffix)
cleanup_groups.append(group)
category={'code':'qa_'+suffix,'title':'Тест маршрутизации '+suffix,'active':True,'expertId':'','groupId':group['id']}
admin.call('/admin/categories',category,expected=204)
cleanup_categories.append(category)
q={'code':'qa_'+suffix,'titleStudent':'Тестовый вопрос','titleAdult':'Тестовый вопрос для взрослого','answerType':'SINGLE','options':['Да','Нет'],'required':False,'active':False,'sortOrder':999}
saved=admin.call('/admin/questions',q,expected=201)
cleanup_questions.append(saved)
assert not saved['active']
saved.update({'answerType':'MULTIPLE','options':['Первый','Второй']})
admin.call('/admin/questions/'+saved['id'],saved,method='PUT',expected=204)
stored=next(v for v in admin.call('/admin/questions')['items'] if v['id']==saved['id'])
assert stored['options']==['Первый','Второй']
assert all(v['id']!=saved['id'] for v in applicant.call('/questionnaire')['items'])
parent={'code':'parent_'+suffix,'titleStudent':'Нужна дополнительная помощь?','titleAdult':'Нужна дополнительная помощь?','answerType':'SINGLE','options':['Да','Нет'],'required':True,'active':True,'sortOrder':997,'showIfQuestionCode':'','showIfValues':[]}
parent=admin.call('/admin/questions',parent,expected=201)
cleanup_questions.append(parent)
child={'code':'child_'+suffix,'titleStudent':'Какая именно помощь?','titleAdult':'Какая именно помощь?','answerType':'SINGLE','options':['Консультация','Разговор'],'required':True,'active':True,'sortOrder':998,'showIfQuestionCode':parent['code'],'showIfValues':['Да']}
child=admin.call('/admin/questions',child,expected=201)
cleanup_questions.append(child)
grandchild={'code':'grandchild_'+suffix,'titleStudent':'Что важно обсудить?','titleAdult':'Что важно обсудить?','answerType':'TEXT','options':[],'required':True,'active':True,'sortOrder':999,'showIfQuestionCode':child['code'],'showIfValues':['Консультация']}
grandchild=admin.call('/admin/questions',grandchild,expected=201)
cleanup_questions.append(grandchild)
hidden=applicant.call('/appeals',{'applicantType':'PARENT','categoryCode':'unknown','body':'Дочерний вопрос сейчас скрыт','answers':questionnaire_answers({parent['code']:'Нет'})},expected=201)
cleanup_codes.append(hidden['accessCode'])
applicant.call('/appeals',{'applicantType':'PARENT','categoryCode':'unknown','body':'Дочерний вопрос должен быть обязательным','answers':questionnaire_answers({parent['code']:'Да'},skip=[child['code']])},expected=422)
applicant.call('/appeals',{'applicantType':'PARENT','categoryCode':'unknown','body':'Вложенный вопрос должен быть обязательным','answers':questionnaire_answers({parent['code']:'Да',child['code']:'Консультация'},skip=[grandchild['code']])},expected=422)
shown=applicant.call('/appeals',{'applicantType':'PARENT','categoryCode':'unknown','body':'Вложенный вопрос заполнен правильно','answers':questionnaire_answers({parent['code']:'Да',child['code']:'Консультация',grandchild['code']:'Нужна консультация'})},expected=201)
cleanup_codes.append(shown['accessCode'])
for qn in [parent,child,grandchild]:
    qn['active']=False
    admin.call('/admin/questions/'+qn['id'],qn,method='PUT',expected=204)
routed=applicant.call('/appeals',{'applicantType':'PARENT','categoryCode':category['code'],'body':'Проверка подсказки маршрутизации','answers':questionnaire_answers()},expected=201)
cleanup_codes.append(routed['accessCode'])
assert routed['appeal']['status']=='NEW'
routed_path='/staff/cases/'+routed['appeal']['id']
hint=operator.call(routed_path).get('routingHint')
assert hint and hint.get('expert_id'),hint
operator.call(routed_path+'/actions',{'action':'assign','expertId':hint['expert_id']},expected=204)
assert 'body' in expert.call(routed_path)
applicant.call('/appeal-sessions',{'accessCode':routed['accessCode']},expected=201)
applicant.call('/my-appeal',method='DELETE',expected=204)
admin.call('/admin/categories',{**category,'active':False,'expertId':''},expected=204)
print('PASS: role matrix, restricted fields, assignment revocation, coexecutor, lifecycle, personal exports, deletion and session revocation')
print('PASS: category routing, nested questionnaire validation, editing and publication')
print('PASS: screenshot upload, server-side image re-encoding and metadata removal')
print('PASS: applicant helpful/not-helpful decision, return to operator and rating')
