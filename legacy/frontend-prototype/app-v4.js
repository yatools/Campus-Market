/* ================================================================
   梧桐墙 v4：账号体系 / 富文本编辑器 / 全站回帖 / 车队增强 /
   反馈系统 / 管理员后台 / 用户后台 / 观察台解码
   ================================================================ */
(function(){
'use strict';

/* ========== CSS ========== */
var css=document.createElement('style');
css.textContent=[
'#m-dynform textarea,.modal textarea{min-height:80px;max-height:220px;resize:vertical}',   /* 修复弹窗 textarea 过高 */
'.modal{max-height:88vh;overflow-y:auto}',
'.ed-bar{display:flex;flex-wrap:wrap;gap:2px;border:1px solid var(--line);border-bottom:none;border-radius:7px 7px 0 0;background:#F4F2E8;padding:4px 6px}',
'.ed-bar button{border:none;background:none;border-radius:5px;padding:3px 8px;font-size:13px;color:#41463B;cursor:pointer}',
'.ed-bar button:hover{background:#E6E3D4}',
'.ed-wrap textarea{border-radius:0 0 7px 7px!important}',
'.ed-hint{font-size:11px;color:var(--ink-soft);margin-top:3px}',
'.md-body h4{font-family:var(--serif);margin:6px 0 2px}',
'.md-body blockquote{border-left:3px solid var(--line);padding-left:10px;color:var(--ink-soft);margin:4px 0}',
'.md-body code{background:#F0EEE2;border-radius:4px;padding:0 5px;font-family:var(--mono);font-size:12px}',
'.md-body img{max-width:100%;border-radius:8px;margin:4px 0}',
'.md-body ul{padding-left:20px}',
'.md-body .formula{font-family:var(--mono);background:#F4F8F2;border-radius:4px;padding:0 6px}',
'.cm-panel{background:#FBFAF3;border:1px solid var(--line);border-radius:8px;padding:10px 12px;margin-top:8px}',
'.cm-item{font-size:12.5px;padding:6px 0;border-bottom:1px dashed var(--line)}',
'.cm-item:last-child{border-bottom:none}',
'.confirm-bar{position:fixed;inset:0;background:rgba(25,35,28,.5);z-index:220;display:flex;align-items:center;justify-content:center}',
'.confirm-box{background:#fff;border-radius:12px;padding:20px 22px;max-width:340px;width:92%;box-shadow:0 12px 40px rgba(0,0,0,.3)}',
'.admin-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(120px,1fr));gap:8px}',
'.admin-stat{background:#FBFAF3;border:1px solid var(--line);border-radius:8px;padding:10px;text-align:center}',
'.admin-stat b{font-family:var(--mono);font-size:20px;display:block;color:var(--board)}'
].join('\n');
document.head.appendChild(css);

var V_LIVE=false, PERMS=[];
function esc2(s){return String(s==null?'':s).replace(/[&<>"]/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]})}

/* ========== Markdown / BBCode 轻量渲染 ========== */
window.mdRender=function(src){
  var t=esc2(src);
  t=t.replace(/\[b\]([\s\S]*?)\[\/b\]/g,'<b>$1</b>').replace(/\[i\]([\s\S]*?)\[\/i\]/g,'<em>$1</em>')
     .replace(/\[url=(https?:[^\]]+)\]([\s\S]*?)\[\/url\]/g,'<a href="$1" target="_blank" rel="noopener">$2</a>');
  t=t.replace(/!\[([^\]]*)\]\((data:image\/[^)]+|https?:[^)]+)\)/g,'<img src="$2" alt="$1">');
  t=t.replace(/\[([^\]]+)\]\((https?:[^)]+)\)/g,'<a href="$2" target="_blank" rel="noopener">$1</a>');
  t=t.replace(/\$\$([^$]+)\$\$/g,'<span class="formula">$1</span>');
  t=t.replace(/`([^`]+)`/g,'<code>$1</code>');
  t=t.replace(/^## (.+)$/gm,'<h4>$1</h4>').replace(/^# (.+)$/gm,'<h4>$1</h4>');
  t=t.replace(/^&gt; (.+)$/gm,'<blockquote>$1</blockquote>');
  t=t.replace(/^\- (.+)$/gm,'<li>$1</li>').replace(/(<li>[\s\S]*?<\/li>)(?!\s*<li>)/g,'<ul>$1</ul>');
  t=t.replace(/\*\*([^*]+)\*\*/g,'<b>$1</b>').replace(/\*([^*\n]+)\*/g,'<em>$1</em>');
  t=t.replace(/\n/g,'<br>');
  return '<span class="md-body">'+t+'</span>';
};

/* ========== 编辑器：工具栏 + 粘贴/拖放图片 ========== */
function insertAt(ta,before,after,ph){
  var s=ta.selectionStart,e=ta.selectionEnd,v=ta.value,sel=v.slice(s,e)||ph||'';
  ta.value=v.slice(0,s)+before+sel+(after||'')+v.slice(e);
  ta.focus();ta.selectionStart=ta.selectionEnd=s+before.length+sel.length+(after||'').length;
  ta.dispatchEvent(new Event('input'));
}
var EMOJIS=['😀','😂','🥹','😍','🤔','😭','🙏','👍','🔥','🎉','🐧','🍉'];
function enhanceTextarea(ta){
  if(!ta||ta.dataset.enhanced)return;ta.dataset.enhanced='1';
  var wrap=document.createElement('div');wrap.className='ed-wrap';
  ta.parentNode.insertBefore(wrap,ta);
  var bar=document.createElement('div');bar.className='ed-bar';
  var btns=[
    ['B','加粗',function(){insertAt(ta,'**','**','加粗文字')}],
    ['I','斜体',function(){insertAt(ta,'*','*','斜体文字')}],
    ['H1','标题一',function(){insertAt(ta,'\n# ','','标题')}],
    ['H2','标题二',function(){insertAt(ta,'\n## ','','小标题')}],
    ['❝','引用',function(){insertAt(ta,'\n> ','','引用内容')}],
    ['🔗','超链接',function(){insertAt(ta,'[','](https://)','链接文字')}],
    ['≡','列表',function(){insertAt(ta,'\n- ','','列表项')}],
    ['ƒ','公式',function(){insertAt(ta,'$$','$$','E=mc^2')}],
    ['🕐','日期时间',function(){insertAt(ta,new Date().toLocaleString('zh-CN'),'')}],
    ['`','代码',function(){insertAt(ta,'`','`','code')}]
  ];
  btns.forEach(function(b){
    var el=document.createElement('button');el.type='button';el.textContent=b[0];el.title=b[1];
    el.onclick=function(ev){ev.preventDefault();b[2]()};bar.appendChild(el);
  });
  var em=document.createElement('button');em.type='button';em.textContent='😀';em.title='emoji';
  em.onclick=function(ev){ev.preventDefault();
    var pick=EMOJIS[Math.floor(Math.random()*EMOJIS.length)];insertAt(ta,pick,'')};
  bar.appendChild(em);
  var img=document.createElement('button');img.type='button';img.textContent='🖼️';img.title='插入图片（或直接粘贴/拖入）';
  var fi=document.createElement('input');fi.type='file';fi.accept='image/*';fi.style.display='none';
  fi.onchange=function(){if(fi.files[0])readImg(fi.files[0],ta)};
  img.onclick=function(ev){ev.preventDefault();fi.click()};
  bar.appendChild(img);bar.appendChild(fi);
  wrap.appendChild(bar);wrap.appendChild(ta);
  var hint=document.createElement('div');hint.className='ed-hint';
  hint.textContent='支持 Markdown / BBCode 排版 · 可直接粘贴或拖入图片';
  wrap.appendChild(hint);
  ta.addEventListener('paste',function(e){
    var items=(e.clipboardData||{}).items||[];
    for(var i=0;i<items.length;i++)if(items[i].type.indexOf('image')===0){e.preventDefault();readImg(items[i].getAsFile(),ta);break}
  });
  ta.addEventListener('dragover',function(e){e.preventDefault()});
  ta.addEventListener('drop',function(e){
    e.preventDefault();
    var f=e.dataTransfer.files[0];
    if(f&&f.type.indexOf('image')===0)readImg(f,ta);
  });
}
function readImg(file,ta){
  if(file.size>400*1024){toast('图片超过 400KB，请压缩后再插入');return}
  var r=new FileReader();
  r.onload=function(){insertAt(ta,'\n![图片]('+r.result+')\n','')};
  r.readAsDataURL(file);
}
/* 自动增强：弹窗中出现的 textarea */
new MutationObserver(function(){
  document.querySelectorAll('.overlay.show textarea, #m-dynform textarea').forEach(enhanceTextarea);
}).observe(document.body,{childList:true,subtree:true,attributes:true,attributeFilter:['class']});
setTimeout(function(){var t=document.getElementById('th-body');if(t)enhanceTextarea(t)},600);

/* ========== 退出编辑确认（是否要放弃您的帖子？） ========== */
function dirtyTextarea(scope){
  var tas=(scope||document).querySelectorAll('.overlay.show textarea,#m-dynform textarea');
  for(var i=0;i<tas.length;i++)if(tas[i].value.trim().length>4)return true;
  return false;
}
window.confirmAbandon=function(onAbandon){
  var bar=document.createElement('div');bar.className='confirm-bar';
  bar.innerHTML='<div class="confirm-box"><p style="font-weight:700;font-size:14.5px">是否要放弃您的帖子？</p>'
    +'<p class="muted" style="margin:4px 0 12px">未发布的内容不会被保存</p>'
    +'<div style="display:flex;gap:10px"><button class="btn warn" id="cb-yes">🗑 舍弃</button><button class="btn ghost" id="cb-no">取消</button></div></div>';
  document.body.appendChild(bar);
  bar.querySelector('#cb-yes').onclick=function(){bar.remove();onAbandon()};
  bar.querySelector('#cb-no').onclick=function(){bar.remove()};
  bar.addEventListener('click',function(e){if(e.target===bar)bar.remove()});
};
/* 拦截弹窗关闭（✕ / 遮罩 / Esc / 取消） */
document.addEventListener('click',function(e){
  var x=e.target.closest('.overlay .x, .overlay .btn.ghost');
  var ovBg=e.target.classList&&e.target.classList.contains('overlay')?e.target:null;
  var target=x?x.closest('.overlay'):ovBg;
  if(!target||!dirtyTextarea(target))return;
  if(x&&x.id==='df-submit')return;
  if(x&&x.textContent.indexOf('取消')===-1&&!x.classList.contains('x'))return; /* 其他 ghost 按钮不拦截 */
  e.stopImmediatePropagation();e.preventDefault();
  confirmAbandon(function(){
    if(target.id==='m-dynform')target.remove();
    else target.classList.remove('show');
    target.querySelectorAll('textarea').forEach(function(t){t.value=''});
  });
},true);
window.addEventListener('keydown',function(e){
  if(e.key!=='Escape')return;
  var open=document.getElementById('m-dynform')||document.querySelector('.overlay.show');
  if(open&&dirtyTextarea(open)){
    e.stopImmediatePropagation();
    confirmAbandon(function(){
      if(open.id==='m-dynform')open.remove();else open.classList.remove('show');
      open.querySelectorAll('textarea').forEach(function(t){t.value=''});
    });
  }
},true);

/* ========== 账号：验证码注册 / 登录后 UI ========== */
window.V4={
  sendCode:function(){
    var email=document.getElementById('reg-email').value.trim();
    if(!email){toast('先填写邮箱');return}
    var btn=document.getElementById('btnSendCode');
    api('/api/auth/send-code','POST',{email:email}).then(function(r){
      toast('验证码已发送到邮箱'+(r.demo_code?'（演示模式已自动填入）':''));
      if(r.demo_code)document.getElementById('reg-code').value=r.demo_code;
      var n=60;btn.disabled=true;
      var timer=setInterval(function(){btn.textContent=n+'s';if(--n<0){clearInterval(timer);btn.disabled=false;btn.textContent='发送验证码'}},1000);
    }).catch(function(e){toast(e.message)});
  },
  doRegister2:function(){
    if(!document.getElementById('reg-agree').checked){
      toast('必须先阅读并勾选《用户协议》与《社区规范》');
      document.querySelector('#m-register .checkline').classList.add('shake');
      setTimeout(function(){document.querySelector('#m-register .checkline').classList.remove('shake')},400);
      return;
    }
    api('/api/auth/register2','POST',{
      email:document.getElementById('reg-email').value,
      code:document.getElementById('reg-code').value,
      password:document.getElementById('reg-pw').value,
      nickname:document.getElementById('reg-nick').value,
      agreed:true
    }).then(function(d){
      window.TOKEN=d.token;localStorage.setItem('wt_token',d.token);
      closeModal('m-register');
      toast('注册成功，已自动登录（身份：'+d.identity+'），正在刷新…');
      setTimeout(function(){location.reload()},900);
    }).catch(function(e){toast(e.message)});
  }
};
function authUI(){
  if(window.TOKEN){
    var reg=document.getElementById('btnReg');if(reg)reg.style.display='none';
  }
}
setTimeout(authUI,500);

/* ========== 全站回帖 ========== */
window.XCM={
  open:function(kind,id,el){
    var card=el.closest('.post,.card,.ticket,div');
    var old=card.querySelector('.cm-panel');
    if(old){old.remove();return}
    var panel=document.createElement('div');panel.className='cm-panel';
    panel.innerHTML='<div class="muted">加载评论中…</div>';
    card.appendChild(panel);
    api('/api/comments?kind='+kind+'&item_id='+id).then(function(list){
      var h='';
      list.forEach(function(c){h+='<div class="cm-item"><b>'+esc2(c.author)+'</b>：'+(window.mdRender||esc2)(c.body)
        +' <span class="muted mono" style="font-size:10px">'+esc2((c.at||'').slice(5,16).replace('T',' '))+'</span></div>'});
      h+='<div style="margin-top:8px"><textarea rows="2" class="cm-input" placeholder="写下你的回帖…（支持 Markdown）" style="width:100%;border:1px solid var(--line);border-radius:7px;padding:8px;background:#fff"></textarea>'
        +'<button class="btn primary sm" style="margin-top:6px">回帖</button></div>';
      panel.innerHTML=(list.length?'':'<div class="muted">还没有回帖，抢个沙发</div>')+h;
      enhanceTextarea(panel.querySelector('.cm-input'));
      panel.querySelector('.btn').onclick=function(){
        if(needLogin())return;
        var body=panel.querySelector('.cm-input').value.trim();
        if(!body){toast('先写点内容');return}
        api('/api/comments','POST',{kind:kind,item_id:id,body:body}).then(function(){
          toast('回帖成功');panel.remove();XCM.open(kind,id,el);
        }).catch(function(e){toast(e.message)});
      };
    }).catch(function(e){panel.innerHTML='<div class="muted">'+esc2(e.message)+'</div>'});
  }
};

/* ========== 车队：确认上车 / 请假 / 下车 / 车头管理 ========== */
var _join=window.joinTeam;
window.joinTeam=function(id){
  if(needLogin())return;
  api('/api/teams').then(function(list){
    var t=list.filter(function(x){return x.id===id})[0];
    if(!t){toast('车队不存在');return}
    var bar=document.createElement('div');bar.className='confirm-bar';
    bar.innerHTML='<div class="confirm-box"><p style="font-weight:700;font-size:15px">确定要上车吗？🚗</p>'
      +'<div style="font-size:12.5px;margin:8px 0;line-height:1.9;background:#FBFAF3;border-radius:8px;padding:8px 12px">'
      +'<b>'+esc2(t.game)+' · '+esc2(t.mode)+'</b><br>发车：'+esc2(t.departure_at.replace('T',' ').slice(5,16))
      +'<br>段位：'+esc2(t.rank_req)+' ｜ 语音：'+esc2(t.voice)
      +'<br>氛围：'+esc2(t.vibe||'—')+'<br>'+esc2(t.newbie)
      +(t.notes?'<br>📌 注意事项：'+esc2(t.notes):'')
      +'<br><span class="muted">上车后发车前 '+t.remind_before+' 分钟提醒；发车前 30 分钟内退出且未请假将扣信用 3 分</span></div>'
      +'<div style="display:flex;gap:10px"><button class="btn primary" id="jt-ok">确认上车</button><button class="btn ghost" id="jt-no">再想想</button></div></div>';
    document.body.appendChild(bar);
    bar.querySelector('#jt-no').onclick=function(){bar.remove()};
    bar.addEventListener('click',function(e){if(e.target===bar)bar.remove()});
    bar.querySelector('#jt-ok').onclick=function(){
      bar.remove();
      api('/api/teams/'+id+'/join','POST',{remind_channels:'邮件,站内'})
        .then(function(d){toast(d.message);if(window.XLOADERS){}; if(typeof loadTeams==='function')loadTeams()})
        .catch(function(e){toast(e.message)});
    };
  }).catch(function(e){toast(e.message)});
};
window.XT={
  excuse:function(id){
    if(needLogin())return;
    api('/api/teams/'+id+'/excuse','POST').then(function(r){toast(r.note)}).catch(function(e){toast(e.message)});
  },
  leave:function(id){
    if(needLogin())return;
    confirmAbandonLite('确定下车吗？发车前 30 分钟内退出且未请假将扣信用 3 分。',function(){
      api('/api/teams/'+id+'/leave','POST').then(function(r){toast(r.message);if(typeof loadTeams==='function')loadTeams()})
        .catch(function(e){toast(e.message)});
    });
  },
  manage:function(id){
    if(needLogin())return;
    formModal('车头管理（仅车头可保存）',[
      {k:'notes',label:'注意事项（保存后推送给全员）',type:'textarea',full:1},
      {k:'remind_before',label:'发车前提醒（分钟）',type:'number',ph:'30'},
      {k:'voice_link',label:'频道链接（KOOK/QQ群/TS）',full:1,ph:'https://...'}
    ],function(v,close){
      var body={};
      if(v.notes)body.notes=v.notes;
      if(v.remind_before)body.remind_before=parseInt(v.remind_before);
      if(v.voice_link)body.voice_link=v.voice_link;
      api('/api/teams/'+id+'/update','POST',body).then(function(){close();toast('已保存并通知车友');if(typeof loadTeams==='function')loadTeams()})
        .catch(function(e){toast(e.message)});
    });
  }
};
function confirmAbandonLite(text,cb){
  var bar=document.createElement('div');bar.className='confirm-bar';
  bar.innerHTML='<div class="confirm-box"><p style="font-size:13.5px">'+esc2(text)+'</p>'
    +'<div style="display:flex;gap:10px;margin-top:12px"><button class="btn warn" id="ca-y">确定</button><button class="btn ghost" id="ca-n">取消</button></div></div>';
  document.body.appendChild(bar);
  bar.querySelector('#ca-y').onclick=function(){bar.remove();cb()};
  bar.querySelector('#ca-n').onclick=function(){bar.remove()};
}

/* ========== 反馈入口 ========== */
window.XF={
  open:function(){
    if(window.LIVE&&!window.TOKEN){toast('提交反馈需要先登录');openModal('m-login');return}
    formModal('💡 反馈 Bug / 建议 / 畅想（被采纳有经验与信用奖励）',[
      {k:'ftype',label:'类型',type:'select',options:['建议','bug','畅想']},
      {k:'title',label:'一句话概括',full:1},
      {k:'body',label:'详细描述（bug 请附复现步骤）',type:'textarea',full:1}
    ],function(v,close){
      api('/api/feedback','POST',v).then(function(r){close();toast(r.note)}).catch(function(e){toast(e.message)});
    });
  }
};

/* ========== 观察台：吃瓜解码开关 ========== */
function injectUncover(){
  var pg=document.getElementById('page-observe');if(!pg||pg.querySelector('.uncover-bar'))return;
  if(!window.TOKEN||!window.ME)return;
  var need=90;
  PERMS.forEach(function(p){if(p.key==='observe_uncover')need=p.need});
  var bar=document.createElement('div');bar.className='rulebox uncover-bar';
  if((window.ME.credit||0)>=need||window.ME.identity==='管理员'){
    bar.innerHTML='<b>🍉 解码资格</b>：你的信用 '+window.ME.credit+' ≥ '+need+'，可解除默认打码。'
      +'<label style="margin-left:8px"><input type="checkbox" id="uncoverChk"> 我同意《吃瓜不扩散协议》：解码内容仅限站内查看，禁止截图外传，违者扣信用并公示</label>';
  }else{
    bar.innerHTML='<b>🍉 解码资格</b>：信用 ≥ '+need+' 且同意《吃瓜不扩散协议》后可解除打码（你当前 '+(window.ME.credit||'—')+' 分）。';
  }
  var d=pg.querySelector('.dyn');
  if(d)pg.insertBefore(bar,d);else pg.appendChild(bar);
  var chk=bar.querySelector('#uncoverChk');
  if(chk){
    api('/api/me/prefs').then(function(p){chk.checked=p.uncover_agreed==='1'}).catch(function(){});
    chk.onchange=function(){
      api('/api/me/prefs','POST',{key:'uncover_agreed',value:chk.checked?'1':'0'}).then(function(){
        toast(chk.checked?'已同意协议，内容已解码 🍉':'已恢复打码');
        if(window.XLOADERS&&XLOADERS.observe)XLOADERS.observe();
      }).catch(function(e){toast(e.message)});
    };
  }
}

/* ========== 用户后台（我的信用页追加） ========== */
function loadUserAdmin(){
  if(!window.TOKEN||!window.ME)return;
  var pg=document.getElementById('page-credit');
  var d=pg.querySelector('.dyn-user');
  if(!d){d=document.createElement('div');d.className='dyn-user';pg.appendChild(d)}
  Promise.all([api('/api/permissions'),api('/api/me/posts'),api('/api/me/prefs')]).then(function(rs){
    var perms=rs[0],posts=rs[1],prefs=rs[2];
    var h='<div class="card"><h3>📈 信用晋升路线</h3>';
    var locked=perms.filter(function(p){return window.ME.credit<p.need});
    if(!locked.length)h+='<p style="font-size:13px">🎉 你已解锁全部信用权限！保持准时发车与优质贡献即可维持。</p>';
    else locked.forEach(function(p){
      h+='<li style="list-style:none;font-size:13px;margin-bottom:4px">🔒 <b>'+esc2(p.name)+'</b>：还差 <b class="mono" style="color:var(--stamp)">'+(p.need-window.ME.credit)+'</b> 分（需 '+p.need+'）</li>';
    });
    h+='<p class="muted" style="margin-top:6px">怎么涨分：准时发车签到 +1/次 · 反馈被采纳 · 失物被认领 +2 · 违规扣分（爽约 −3、禁售 −10）</p></div>';
    h+='<div class="card"><h3>🔐 账号与隐私</h3>'
      +'<div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:8px">'
      +'<button class="btn ghost sm" onclick="XU.pw()">修改密码</button>'
      +'<button class="btn ghost sm" onclick="XU.email()">更换邮箱</button></div>'
      +'<label style="font-size:13px"><input type="checkbox" id="prefDm" '+(prefs.dm_stranger_off==='1'?'checked':'')+'> 关闭陌生人私信（仅车队/交易对象可私信我）</label>'
      +'<p class="muted">当前绑定：'+esc2(window.ME.email||'—')+'</p></div>';
    h+='<div class="card"><h3>🗂 我的帖子（'+posts.length+'）</h3>';
    if(!posts.length)h+='<p class="muted">还没有发过内容</p>';
    posts.slice(0,10).forEach(function(p){
      h+='<div style="display:flex;gap:8px;align-items:center;font-size:12.5px;padding:5px 0;border-bottom:1px dashed var(--line)">'
        +'<span class="tag gray">'+esc2(p.board)+'</span><span style="flex:1">'+esc2(p.title)+'</span>'
        +'<button class="btn ghost sm" onclick="XU.del(\''+p.kind+'\','+p.id+')">删除</button></div>';
    });
    h+='</div>';
    d.innerHTML=h;
    var dm=d.querySelector('#prefDm');
    if(dm)dm.onchange=function(){
      api('/api/me/prefs','POST',{key:'dm_stranger_off',value:dm.checked?'1':'0'})
        .then(function(){toast(dm.checked?'已关闭陌生人私信':'已开启陌生人私信')}).catch(function(e){toast(e.message)});
    };
  }).catch(function(){});
}
window.XU={
  pw:function(){
    formModal('修改密码',[
      {k:'old',label:'原密码',type:'password'},
      {k:'new',label:'新密码（至少 6 位）',type:'password'}
    ],function(v,close){
      api('/api/me/password','POST',v).then(function(){close();toast('密码已更新，下次登录生效')}).catch(function(e){toast(e.message)});
    });
  },
  email:function(){
    formModal('更换邮箱（先向新邮箱发送验证码）',[
      {k:'new_email',label:'新邮箱',full:1},
      {k:'code',label:'验证码（在下方按钮发送）'}
    ],function(v,close){
      api('/api/me/email','POST',v).then(function(){close();toast('邮箱已更换')}).catch(function(e){toast(e.message)});
    });
    setTimeout(function(){
      var m=document.querySelector('#m-dynform .actions');
      var b=document.createElement('button');b.className='btn ghost';b.textContent='发送验证码';
      b.onclick=function(){
        var em=document.getElementById('df-new_email').value;
        api('/api/auth/send-code','POST',{email:em}).then(function(r){
          toast('验证码已发送'+(r.demo_code?'（演示自动填入）':''));
          if(r.demo_code)document.getElementById('df-code').value=r.demo_code;
        }).catch(function(e){toast(e.message)});
      };
      m.insertBefore(b,m.firstChild);
    },100);
  },
  del:function(kind,id){
    confirmAbandonLite('确定删除这条内容吗？不可恢复。',function(){
      api('/api/me/delete','POST',{kind:kind,item_id:id}).then(function(){toast('已删除');loadUserAdmin()})
        .catch(function(e){toast(e.message)});
    });
  }
};

/* ========== 管理员后台 ========== */
function buildAdminPage(){
  if(document.getElementById('page-adminx'))return;
  var sec=document.createElement('section');sec.className='page';sec.id='page-adminx';
  sec.innerHTML='<div class="page-head"><h2>🛠 管理员后台</h2><p>网站信息 · 内容 · 用户 · 公告 · 权限 · 板块 · 备份 · 清理</p></div><div class="dyn-admin"></div>';
  document.querySelector('.main').appendChild(sec);
  var nav=document.createElement('button');nav.className='navitem';nav.dataset.page='adminx';
  nav.innerHTML='<span class="ico">🛠</span>管理后台<span class="mini-badge">admin</span>';
  nav.onclick=function(){showPage('adminx');loadAdmin()};
  document.querySelector('.sidenav').appendChild(nav);
}
function loadAdmin(){
  var d=document.querySelector('#page-adminx .dyn-admin');if(!d)return;
  d.innerHTML='<div class="skel" style="height:80px"></div>';
  Promise.all([api('/api/admin/overview'),api('/api/admin/users'),api('/api/admin/settings'),api('/api/feedback')]).then(function(rs){
    var ov=rs[0],users=rs[1],st=rs[2],fb=rs[3];
    var h='<div class="card"><h3>📊 站点概览</h3><div class="admin-grid">';
    Object.keys(ov).forEach(function(k){h+='<div class="admin-stat"><b>'+ov[k]+'</b><span class="muted">'+esc2(k)+'</span></div>'});
    h+='</div></div>';
    /* 用户管理 */
    h+='<div class="card"><h3>👥 用户管理</h3><table class="gov"><tr><th>ID</th><th>昵称</th><th>身份</th><th>信用</th><th>操作</th></tr>';
    users.slice(0,10).forEach(function(u){
      h+='<tr><td class="mono">'+u.id+'</td><td>'+esc2(u.nickname)+'</td>'
        +'<td><select id="au-i-'+u.id+'" style="border:1px solid var(--line);border-radius:5px">'
        +['已认证学生','未认证访客','校友','导员','管理员'].map(function(x){return '<option'+(x===u.identity?' selected':'')+'>'+x+'</option>'}).join('')+'</select></td>'
        +'<td><input id="au-c-'+u.id+'" type="number" value="'+u.credit+'" style="width:56px;border:1px solid var(--line);border-radius:5px;padding:2px 4px"></td>'
        +'<td><button class="btn ghost sm" onclick="XAdm.saveUser('+u.id+')">保存</button></td></tr>';
    });
    h+='</table></div>';
    /* 内容管理 */
    h+='<div class="card"><h3>🧹 内容管理</h3><div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;font-size:13px">'
      +'类型 <select id="adm-kind" style="border:1px solid var(--line);border-radius:5px;padding:3px">'
      +['post','listing','team','question','hb','activity','lost','observe','comment','review'].map(function(x){return '<option>'+x+'</option>'}).join('')+'</select>'
      +' ID <input id="adm-id" type="number" style="width:70px;border:1px solid var(--line);border-radius:5px;padding:3px">'
      +' 事由 <input id="adm-reason" style="width:160px;border:1px solid var(--line);border-radius:5px;padding:3px" placeholder="将进入治理公示">'
      +' <button class="btn warn sm" onclick="XAdm.del()">删除内容</button></div>'
      +'<p class="muted" style="margin-top:6px">各板块卡片右下角 ID 即内容 ID；删除即时生效并可选记入治理公示。</p></div>';
    /* 公告发布 */
    h+='<div class="card"><h3>📢 发布公告</h3><div style="display:flex;gap:8px;flex-wrap:wrap">'
      +'<input id="an-title" placeholder="公告标题" style="flex:1;min-width:160px;border:1px solid var(--line);border-radius:6px;padding:6px">'
      +'<select id="an-level" style="border:1px solid var(--line);border-radius:6px"><option>普通</option><option>强提醒</option></select></div>'
      +'<textarea id="an-body" rows="2" placeholder="公告正文" style="width:100%;margin-top:8px;border:1px solid var(--line);border-radius:6px;padding:6px"></textarea>'
      +'<button class="btn primary sm" style="margin-top:6px" onclick="XAdm.announce()">发布</button></div>';
    /* 权限与板块设置 */
    h+='<div class="card"><h3>⚙️ 权限阈值与板块设置</h3><div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:8px;font-size:12.5px">';
    [['perm_anon_post','匿名发帖信用'],['perm_trade','交易发帖信用'],['perm_observe','观察台发帖信用'],
     ['perm_contact','发联系方式信用'],['perm_create_team','建车队信用'],['perm_course_review','课评信用'],
     ['observe_uncover_credit','观察台解码信用'],['auto_clean','自动清理(1开0关)']].forEach(function(x){
      var val=st[x[0]]!==undefined?st[x[0]]:'';
      h+='<label>'+x[1]+'<input id="st-'+x[0]+'" value="'+esc2(val)+'" placeholder="默认" style="width:100%;border:1px solid var(--line);border-radius:5px;padding:3px 6px"></label>';
    });
    h+='</div><label style="font-size:12.5px;display:block;margin-top:8px">生存手册分类（JSON 数组，即图六板块的增删改）'
      +'<textarea id="st-hb_categories" rows="2" style="width:100%;border:1px solid var(--line);border-radius:6px;padding:6px;font-family:var(--mono);font-size:11px">'+esc2(st.hb_categories||'[]')+'</textarea></label>'
      +'<button class="btn primary sm" style="margin-top:6px" onclick="XAdm.saveSettings()">保存全部设置</button></div>';
    /* 反馈处理 */
    h+='<div class="card"><h3>💡 反馈处理</h3>';
    var pending=fb.filter(function(f){return f.status==='待处理'});
    if(!pending.length)h+='<p class="muted">暂无待处理反馈</p>';
    pending.forEach(function(f){
      h+='<div style="border:1px dashed var(--line);border-radius:8px;padding:8px 10px;margin-bottom:8px;font-size:12.5px">'
        +'<span class="tag yellow">'+esc2(f.ftype)+'</span><b>'+esc2(f.title)+'</b>（'+esc2(f.author)+'）<br>'+esc2(f.body)
        +'<div style="display:flex;gap:6px;margin-top:6px;align-items:center">奖励 exp<input id="fb-e-'+f.id+'" type="number" value="20" style="width:52px;border:1px solid var(--line);border-radius:5px">'
        +' 信用<input id="fb-c-'+f.id+'" type="number" value="1" style="width:44px;border:1px solid var(--line);border-radius:5px">'
        +'<button class="btn primary sm" onclick="XAdm.fb('+f.id+',\'已采纳\')">采纳并奖励</button>'
        +'<button class="btn ghost sm" onclick="XAdm.fb('+f.id+',\'已拒绝\')">拒绝</button></div></div>';
    });
    h+='</div>';
    /* 维护 */
    h+='<div class="card"><h3>🧰 维护</h3><div style="display:flex;gap:10px;flex-wrap:wrap">'
      +'<button class="btn ghost sm" onclick="XAdm.backup()">⬇️ 下载数据库备份</button>'
      +'<button class="btn ghost sm" onclick="XAdm.clean()">🧹 立即清理过期内容</button></div>'
      +'<p class="muted" style="margin-top:6px">自动清理开启时每小时清一次过期匿名帖与验证码。</p></div>';
    d.innerHTML=h;
  }).catch(function(e){d.innerHTML='<p class="muted">'+esc2(e.message)+'</p>'});
}
window.XAdm={
  saveUser:function(id){
    api('/api/admin/users/'+id,'POST',{
      credit:parseInt(document.getElementById('au-c-'+id).value),
      identity:document.getElementById('au-i-'+id).value
    }).then(function(){toast('已保存')}).catch(function(e){toast(e.message)});
  },
  del:function(){
    api('/api/admin/delete','POST',{
      kind:document.getElementById('adm-kind').value,
      item_id:parseInt(document.getElementById('adm-id').value),
      reason:document.getElementById('adm-reason').value
    }).then(function(){toast('已删除')}).catch(function(e){toast(e.message)});
  },
  announce:function(){
    api('/api/admin/announce','POST',{
      title:document.getElementById('an-title').value,
      body:document.getElementById('an-body').value,
      level:document.getElementById('an-level').value
    }).then(function(){toast('公告已发布')}).catch(function(e){toast(e.message)});
  },
  saveSettings:function(){
    var keys=['perm_anon_post','perm_trade','perm_observe','perm_contact','perm_create_team',
              'perm_course_review','observe_uncover_credit','auto_clean','hb_categories'];
    var chain=Promise.resolve();
    keys.forEach(function(k){
      var el=document.getElementById('st-'+k);
      if(el&&el.value!==''){chain=chain.then(function(){return api('/api/admin/settings','POST',{key:k,value:el.value})})}
    });
    chain.then(function(){toast('设置已保存，即时生效')}).catch(function(e){toast(e.message)});
  },
  fb:function(id,status){
    api('/api/admin/feedback/'+id,'POST',{
      status:status,
      reward_exp:parseInt((document.getElementById('fb-e-'+id)||{}).value||'0'),
      reward_credit:parseInt((document.getElementById('fb-c-'+id)||{}).value||'0')
    }).then(function(){toast('已处理');loadAdmin()}).catch(function(e){toast(e.message)});
  },
  backup:function(){
    fetch('/api/admin/backup',{headers:{Authorization:'Bearer '+window.TOKEN}}).then(function(r){
      if(!r.ok)throw new Error('备份失败');return r.blob();
    }).then(function(b){
      var a=document.createElement('a');a.href=URL.createObjectURL(b);
      a.download='wutong-backup-'+new Date().toISOString().slice(0,10)+'.db';a.click();
      toast('备份已开始下载');
    }).catch(function(e){toast(e.message)});
  },
  clean:function(){
    api('/api/admin/clean','POST').then(function(r){toast('清理完成：'+JSON.stringify(r.deleted))}).catch(function(e){toast(e.message)});
  }
};

/* ========== 手册分类动态化（管理员改完即刻生效） ========== */
function loadHbCats(){
  api('/api/handbook-categories').then(function(cats){
    var grid=document.querySelector('#page-handbook .hb-grid');
    if(!grid)return;
    grid.innerHTML=cats.map(function(c){
      return '<div class="hb-item" onclick="XHB.cat(\''+esc2(c)+'\')"><span class="ico">📁</span>'+esc2(c)+'</div>';
    }).join('');
  }).catch(function(){});
}
window.XHB={
  cat:function(c){
    api('/api/handbook?category='+encodeURIComponent(c)).then(function(list){
      var d=document.querySelector('#page-handbook .dyn');
      if(!d)return;
      var h='<div class="card"><h3>📁 '+esc2(c)+'（'+list.length+' 篇）<button class="btn ghost sm" style="margin-left:auto" onclick="if(window.XLOADERS)XLOADERS.handbook()">返回全部</button></h3>';
      if(!list.length)h+='<p class="muted">这个分类还没有文章，点上方「投稿经验帖」写第一篇！</p>';
      list.forEach(function(a){
        h+='<div style="display:flex;gap:8px;align-items:center;padding:8px 0;border-bottom:1px dashed var(--line)">'
          +(a.featured?'<span class="stamp-badge">精</span>':'')+'<b style="font-size:13.5px">'+esc2(a.title)+'</b>'
          +'<span class="muted" style="margin-left:auto">'+esc2(a.author)+' · ⭐'+a.favs+'</span>'
          +'<button class="btn ghost sm" onclick="XH.fav('+a.id+')">收藏</button></div>';
      });
      h+='</div>';
      d.innerHTML=h;
    }).catch(function(e){toast(e.message)});
  }
};

/* ========== 路由 hook：观察台注入解码条 / 用户后台 / 手册分类 ========== */
var _sp4=window.showPage;
window.showPage=function(id){
  _sp4(id);
  if(!V_LIVE)return;
  if(id==='observe')setTimeout(injectUncover,600);
  if(id==='credit')setTimeout(loadUserAdmin,300);
  if(id==='handbook')setTimeout(loadHbCats,400);
  if(id==='adminx')loadAdmin();
};

/* ========== 启动 ========== */
setTimeout(function(){
  api('/api/permissions').then(function(p){
    V_LIVE=true;PERMS=p;
    if(window.TOKEN){
      var wait=setInterval(function(){
        if(window.ME){clearInterval(wait);
          if(window.ME.identity==='管理员')buildAdminPage();
        }
      },300);
      setTimeout(function(){clearInterval(wait)},5000);
    }
    console.log('[梧桐墙] v4 已加载：编辑器/回帖/车队增强/后台');
  }).catch(function(){console.log('[梧桐墙] v4 静态模式')});
},300);
})();
