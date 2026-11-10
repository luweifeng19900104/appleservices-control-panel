'use strict';
const authToken=document.querySelector('meta[name="auth-token"]').content;
const loading=document.getElementById('loadingState');
const setupForm=document.getElementById('setupForm');
const loginForm=document.getElementById('loginForm');
const message=document.getElementById('authMessage');

function showMessage(text){message.textContent=text;message.hidden=false;}
function clearMessage(){message.textContent='';message.hidden=true;}
function showForm(form){loading.hidden=true;setupForm.hidden=form!==setupForm;loginForm.hidden=form!==loginForm;form.querySelector('input')?.focus();}
async function authRequest(path,data){
  const headers={'X-Auth-Token':authToken};
  if(data!==undefined)headers['Content-Type']='application/json';
  const response=await fetch('/auth/'+path,{method:data===undefined?'GET':'POST',headers,body:data===undefined?undefined:JSON.stringify(data),cache:'no-store'});
  let result={};
  try{result=await response.json();}catch{}
  if(!response.ok)throw new Error(result.message||'操作未完成，请稍后重试');
  return result;
}

async function initialize(){
  try{
    const status=await authRequest('status');
    if(status.authenticated){location.replace('/');return;}
    showForm(status.configured?loginForm:setupForm);
  }catch(error){loading.hidden=true;showMessage(error.message);}
}

setupForm.addEventListener('submit',async event=>{
  event.preventDefault();clearMessage();
  const password=document.getElementById('setupPassword').value;
  const confirm=document.getElementById('setupConfirm').value;
  if(password!==confirm){showMessage('两次输入的管理密码不一致');return;}
  const button=setupForm.querySelector('button');button.disabled=true;
  try{await authRequest('setup',{password,confirm});location.replace('/');}
  catch(error){showMessage(error.message);document.getElementById('setupPassword').focus();}
  finally{button.disabled=false;document.getElementById('setupPassword').value='';document.getElementById('setupConfirm').value='';}
});

loginForm.addEventListener('submit',async event=>{
  event.preventDefault();clearMessage();
  const input=document.getElementById('loginPassword');
  const password=input.value;input.value='';
  const button=loginForm.querySelector('button');button.disabled=true;
  try{await authRequest('login',{password});location.replace('/');}
  catch(error){showMessage(error.message);input.focus();}
  finally{button.disabled=false;}
});

initialize();
