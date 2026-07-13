/* ================================================================
   梧桐墙 扩展前端 v2：问答/手册/课评/观察台/治理/失物/活动/公告/私信/头衔
   依赖 index.html 主脚本中的 api()/esc()/toast()/needLogin()/openModal 等。
   后端不可用时不做任何事（静态演示模式）。
   ================================================================ */
(function(){
'use strict';
var X_LIVE=false;

/* ---------- 通用：动态区域 ---------- */
function dynArea(page,removeSel){
  var pg=document.getElementById('page-'+page);
  if(!pg)return null;
  if(removeSel&&!pg.dataset.cleaned){pg.querySelectorAll(removeSel).forEach(function(e){e.remove()});pg.dataset.cleaned='1'}
  var d=pg.querySelector('.dyn');
  if(!d){d=document.createElement('div');d.className='dyn';
    var ph=pg.querySelector('.page-head');
    if(ph)ph.after(d);else pg.prepend(d)}
  return d;
}

/* ---------- 通用：动态表单弹窗 ---------- */
function formModal(title,fields,onsubmit){
  var old=document.getElementById('m-dynform');if(old)old.remove();
  var ov=document.createElement('div');ov.className='overlay show';ov.id='m-dynform';
  var h='<div class="modal" style="max-width:520px"><button class="x" onclick="document.getElementById(\'m-dynform\').remove()">✕</button><h3>'+esc(title)+'</h3><div class="fgrid">';
  fields.forEach(function(f){
    h+='<div class="field'+(f.full?' full':'')+'"><label>'+esc(f.label)+'</label>';
    if(f.type==='textarea')h+='<textarea rows="3" id="df-'+f.k+'" placeholder="'+esc(f.ph||'')+'"></textarea>';
    else if(f.type==='select'){h+='<select id="df-'+f.k+'">';(f.options||[]).forEach(function(o){h+='<option>'+esc(o)+'</option>'});h+='</select>'}
    else h+='<input id="df-'+f.k+'" type="'+(f.type||'text')+'" placeholder="'+esc(f.ph||'')+'">';
    h+='</div>';
  });
  h+='</div><div class="actions"><button class="btn ghost" onclick="document.getElementById(\'m-dynform\').remove()">取消</button><button class="btn primary" id="df-submit">发布</button></div></div>';
  ov.innerHTML=h;
  ov.addEventListener('click',function(e){if(e.target===ov)ov.remove()});
  document.body.appendChild(ov);
  document.getElementById('df-submit').onclick=function(){
    var vals={};fields.forEach(function(f){vals[f.k]=document.getElementById('df-'+f.k).value});
    onsubmit(vals,function(){ov.remove()});
  };
}
window.formModal=formModal;
function toolbar(area,btnText,onclick,extraHtml){
  var t='<div style="display:flex;gap:10px;align-items:center;margin-bottom:12px;flex-wrap:wrap">'
    +'<button class="btn primary sm" id="tb-btn">'+esc(btnText)+'</button>'+(extraHtml||'')+'</div>';
  return t;
}

/* ================= 问答 ================= */
function loadQA(){
  api('/api/qa').then(function(list){
    var d=dynArea('qa','.card');if(!d)return;
    var h=toolbar(d,'✋ 我要提问');
    list.forEach(function(q){
      h+='<div class="card"><div class="p-head" style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">'
        +(q.bounty?'<span class="tag red">悬赏 '+q.bounty+' 分</span>':'')
        +'<span class="tag gray">'+esc(q.category)+'</span>'
        +(q.tags?'<span class="tag gray">'+esc(q.tags)+'</span>':'')
        +(q.best?'<span class="tag green">✅ 已采纳</span>':'<span class="tag yellow">'+q.n_answers+' 个回答</span>')
        +'</div><div class="p-title" style="font-weight:700;font-size:15px;margin:6px 0">'+esc(q.title)+'</div>'
        +(q.body?'<div class="p-body" style="font-size:13px">'+esc(q.body)+'</div>':'');
      if(q.best){
        h+='<div style="background:#F4F8F2;border-left:3px solid var(--leaf);border-radius:6px;padding:10px 12px;font-size:13px;margin-top:8px">'
          +'<b style="color:#2C5E44">✅ 最佳答案</b>（'+esc(q.best.author)+'）：'+esc(q.best.body)
          +'<div class="muted" style="margin-top:4px">👍 '+q.best.likes+' · 采纳 +'+(20+q.bounty)+' 经验</div></div>';
      }
      h+='<div class="p-foot" style="display:flex;gap:16px;margin-top:10px;color:var(--ink-soft);font-size:12px">'
        +'<span style="cursor:pointer" onclick="XQ.answer('+q.id+')">✍️ 回答</span>'
        +'<span>提问者：'+esc(q.asker)+'</span></div></div>';
    });
    d.innerHTML=h;
    document.getElementById('tb-btn').onclick=XQ.ask;
  }).catch(function(){});
}
window.XQ={
  ask:function(){
    if(needLogin())return;
    formModal('提问（回答被采纳后答主获得 20+悬赏 经验）',[
      {k:'title',label:'问题标题',full:1,ph:'一句话说清你的问题'},
      {k:'body',label:'补充说明',type:'textarea',full:1},
      {k:'category',label:'分类',type:'select',options:['学院','校区','课程','宿舍','行政事务','其他']},
      {k:'bounty',label:'悬赏积分（0-200）',type:'number',ph:'0'},
      {k:'tags',label:'标签',full:1,ph:'逗号分隔，如：打印,加急'}
    ],function(v,close){
      api('/api/qa','POST',{title:v.title,body:v.body,category:v.category,bounty:parseInt(v.bounty||'0'),tags:v.tags})
        .then(function(){close();toast('问题已发布');loadQA()}).catch(function(e){toast(e.message)});
    });
  },
  answer:function(qid){
    if(needLogin())return;
    formModal('回答问题',[{k:'body',label:'你的回答',type:'textarea',full:1,ph:'具体、可操作的回答更容易被采纳'}],
      function(v,close){
        api('/api/qa/'+qid+'/answer','POST',{body:v.body})
          .then(function(){close();toast('回答已提交');loadQA()}).catch(function(e){toast(e.message)});
      });
  }
};

/* ================= 生存手册 ================= */
function loadHandbook(){
  api('/api/handbook').then(function(list){
    var d=dynArea('handbook');if(!d)return;
    var h=toolbar(d,'📝 投稿经验帖','', '<span class="muted">经验只来自被收藏/采纳/加精，灌水无效</span>');
    h+='<div class="card"><h3>📚 手册文章</h3>';
    list.forEach(function(a){
      h+='<div style="display:flex;gap:8px;align-items:center;padding:8px 0;border-bottom:1px dashed var(--line);flex-wrap:wrap">'
        +(a.featured?'<span class="stamp-badge">精</span>':'')
        +'<b style="font-size:13.5px">'+esc(a.title)+'</b>'
        +'<span class="tag gray">'+esc(a.category)+'</span>'
        +'<span class="muted" style="margin-left:auto">'+esc(a.author)+' · ⭐'+a.favs+'</span>'
        +'<button class="btn ghost sm" onclick="XH.fav('+a.id+')">收藏</button>'
        +'<button class="btn ghost sm" onclick="XCM.open(\'hb\','+a.id+',this)">💬</button></div>';
    });
    h+='</div>';
    d.innerHTML=h;
    document.getElementById('tb-btn').onclick=XH.post;
  }).catch(function(){});
}
window.XH={
  post:function(){
    if(needLogin())return;
    formModal('投稿生存手册',[
      {k:'category',label:'分类',type:'select',options:['新生入学指南','选课指南','宿舍避坑','食堂/外卖评价','校园地图与隐藏地点','实验室/办事流程','奖学金/竞赛/保研/考研','社团体验','打印/维修/快递','校医院攻略','毕业手续指南']},
      {k:'title',label:'标题',full:1},
      {k:'body',label:'正文（≥10 字）',type:'textarea',full:1}
    ],function(v,close){
      api('/api/handbook','POST',v).then(function(r){close();toast(r.note||'已发布');loadHandbook()})
        .catch(function(e){toast(e.message)});
    });
  },
  fav:function(id){
    if(needLogin())return;
    api('/api/handbook/'+id+'/fav','POST').then(function(){toast('已收藏，作者经验 +2');loadHandbook()})
      .catch(function(e){toast(e.message)});
  }
};

/* ================= 课程评价 ================= */
function loadCourses(){
  api('/api/courses').then(function(list){
    var d=dynArea('course','.card');if(!d)return;
    var h=toolbar(d,'✏️ 写课评');
    list.forEach(function(c){
      h+='<div class="card"><div style="display:flex;gap:16px;align-items:center;flex-wrap:wrap">'
        +'<div style="flex:1;min-width:220px"><b style="font-size:15px">'+esc(c.course)+'（'+esc(c.teacher)+'班）</b>'
        +'<div class="muted">'+c.n+' 条评价</div>'
        +(c.top_tags.length?'<p style="font-size:12.5px;margin-top:6px">'+c.top_tags.map(function(t){return '<span class="tag green">'+esc(t)+'</span>'}).join('')+'</p>':'')
        +'</div>';
      if(c.score!==null)h+='<div style="text-align:center"><div class="score-big">'+c.score+'</div><div class="muted">综合评分</div></div>';
      else h+='<div><span class="score-hidden">'+esc(c.hidden_reason)+'</span></div>';
      h+='</div>';
      c.latest.forEach(function(r){
        h+='<div style="font-size:12.5px;border-top:1px dashed var(--line);padding-top:6px;margin-top:6px">'+'★'.repeat(r.rating)+' '+esc(r.body)
          +(r.correction?'<div class="muted" style="margin-top:2px">📎 老师更正：'+esc(r.correction)+'</div>':'')+'</div>';
      });
      h+='</div>';
    });
    d.innerHTML=h;
    document.getElementById('tb-btn').onclick=XC.review;
  }).catch(function(){});
}
window.XC={
  review:function(){
    if(needLogin())return;
    formModal('写课程评价（同一课程同一学期限一次）',[
      {k:'course',label:'课程名'},
      {k:'teacher',label:'老师'},
      {k:'semester',label:'学期',ph:'2026春'},
      {k:'rating',label:'评分 1-5',type:'number',ph:'5'},
      {k:'tags',label:'标签',full:1,ph:'给分好,板书清晰'},
      {k:'body',label:'评价（基于课程体验，禁止人身攻击）',type:'textarea',full:1}
    ],function(v,close){
      api('/api/courses/review','POST',{course:v.course,teacher:v.teacher,semester:v.semester||'2026春',
        rating:parseInt(v.rating||'5'),tags:v.tags,body:v.body})
        .then(function(){close();toast('评价已提交');loadCourses()}).catch(function(e){toast(e.message)});
    });
  }
};

/* ================= 文明观察台 ================= */
function loadObserve(){
  api('/api/observe').then(function(list){
    var d=dynArea('observe','article');if(!d)return;
    var h=toolbar(d,'🔍 发观察帖（需信用 ≥ 75）');
    list.forEach(function(o){
      var st=o.status==='已公示'?'<span class="tag green">✅ 已公示</span>'
        :o.status==='审核中'?'<span class="tag yellow">⏳ 人工审核中（仅你可见）</span>'
        :'<span class="tag red">'+esc(o.status)+'</span>';
      h+='<article class="post"><div class="p-head"><div class="p-avatar">匿</div><span class="p-name">匿名同学</span>'+st
        +(o.uncovered?'<span class="tag red">🍉 已解码（勿扩散）</span>':'')+'</div>'
        +'<div class="p-title">'+esc(o.title)+'</div><div class="p-body">'+esc(o.body)+'</div>'
        +(o.response?'<div class="p-body" style="margin-top:6px"><b>被指认方回应：</b>'+esc(o.response)+'</div>':'')
        +(o.admin_note?'<div class="muted" style="margin-top:4px"><b>管理员备注：</b>'+esc(o.admin_note)+'</div>':'')
        +(o.status!=='审核中'?'<div class="p-foot"><span style="cursor:pointer" onclick="XCM.open(\'observe\','+o.id+',this)">💬 回帖</span></div>':'')
        +'</article>';
    });
    d.innerHTML=h;
    document.getElementById('tb-btn').onclick=XO.post;
  }).catch(function(){});
}
window.XO={
  post:function(){
    if(needLogin())return;
    formModal('发观察帖（先读区规：只描述事件，禁止个人信息与煽动表达）',[
      {k:'title',label:'事件标题',full:1},
      {k:'body',label:'事件描述（数字串会自动打码）',type:'textarea',full:1}
    ],function(v,close){
      api('/api/observe','POST',v).then(function(r){close();toast(r.note);loadObserve()})
        .catch(function(e){toast(e.message)});
    });
  }
};

/* ================= 治理公示 ================= */
function loadGov(){
  api('/api/gov').then(function(list){
    var d=dynArea('gov','.card');if(!d)return;
    var h='<div class="card"><table class="gov"><tr><th>匿名化账号</th><th>违规类型</th><th>处理结果</th><th>规则依据</th><th>时间</th><th>申诉</th></tr>';
    list.forEach(function(p){
      h+='<tr><td class="mono">'+esc(p.masked)+'</td><td>'+esc(p.vtype)+'</td><td>'+esc(p.result)+'</td><td>'+esc(p.rule)+'</td>'
        +'<td class="mono">'+esc((p.created_at||'').slice(5,10))+'</td>'
        +'<td>'+(p.appeal==='可申诉'?'<button class="btn ghost sm" onclick="XG.appeal('+p.id+')">申诉</button>':'<span class="tag gray">'+esc(p.appeal)+'</span>')+'</td></tr>';
    });
    h+='</table><p class="muted" style="margin-top:8px">📚 判例库：每条处罚附案例说明，同案同判。</p></div>';
    d.innerHTML=h;
  }).catch(function(){});
}
window.XG={appeal:function(id){
  if(needLogin())return;
  api('/api/gov/'+id+'/appeal','POST').then(function(r){toast(r.note);loadGov()}).catch(function(e){toast(e.message)});
}};

/* ================= 失物招领 ================= */
function loadLost(){
  api('/api/lost').then(function(list){
    var d=dynArea('lost','article');if(!d)return;
    var h='';
    list.forEach(function(l){
      h+='<article class="post"><div class="p-head">'
        +'<span class="tag '+(l.kind==='捡到'?'green':'red')+'">'+esc(l.kind)+'</span>'
        +'<span class="tag '+(l.status==='已认领'?'blue':'yellow')+'">'+(l.status==='已认领'?'✅ 已认领':(l.kind==='捡到'?'待认领':'寻找中'))+'</span>'
        +'<span class="p-time">'+esc(l.happened_at)+'</span></div>'
        +'<div class="p-title">'+esc(l.item)+'</div>'
        +'<div class="p-body">地点：'+esc(l.place)+' ｜ 联系方式：'+esc(l.contact)+'</div>'
        +(l.status!=='已认领'?'<div class="p-foot"><span style="cursor:pointer" onclick="XL.claim('+l.id+')">🙋 '+(l.kind==='捡到'?'这是我的，认领':'我捡到了，联系失主')+'</span></div>':'')
        +'</article>';
    });
    d.innerHTML=h;
    // 接管页头“+ 登记”按钮
    var pg=document.getElementById('page-lost');
    var btn=pg.querySelector('.page-head .btn');
    if(btn)btn.onclick=XL.post;
  }).catch(function(){});
}
window.XL={
  post:function(){
    if(needLogin())return;
    formModal('登记失物/招领',[
      {k:'kind',label:'类型',type:'select',options:['捡到','丢失']},
      {k:'item',label:'物品'},
      {k:'place',label:'地点',full:1},
      {k:'happened_at',label:'时间',ph:'2026-07-06'},
      {k:'contact',label:'联系方式',ph:'站内私信'}
    ],function(v,close){
      api('/api/lost','POST',v).then(function(){close();toast('已登记');loadLost()}).catch(function(e){toast(e.message)});
    });
  },
  claim:function(id){
    if(needLogin())return;
    api('/api/lost/'+id+'/claim','POST').then(function(){toast('已标记认领，拾主信用 +2');loadLost()})
      .catch(function(e){toast(e.message)});
  }
};

/* ================= 校园活动 ================= */
function loadActivity(){
  api('/api/activity').then(function(list){
    var d=dynArea('activity','article');if(!d)return;
    var h=toolbar(d,'🎪 发布活动/找搭子');
    list.forEach(function(a){
      h+='<article class="post"><div class="p-head"><div class="p-avatar">'+esc(a.nickname[0])+'</div>'
        +'<span class="p-name">'+esc(a.nickname)+'</span><span class="tag blue">'+esc(a.category)+'</span>'
        +'<span class="p-time">'+esc((a.created_at||'').slice(5,10))+'</span></div>'
        +'<div class="p-title">'+esc(a.title)+'</div>'
        +(a.body?'<div class="p-body">'+esc(a.body)+'</div>':'')
        +'<div class="p-foot"><span style="cursor:pointer" onclick="XA.join('+a.id+')">🙋 加入（'+a.joins+'）</span>'
        +'<span style="cursor:pointer" onclick="XCM.open(\'activity\','+a.id+',this)">💬 回帖</span></div></article>';
    });
    d.innerHTML=h;
    document.getElementById('tb-btn').onclick=XA.post;
  }).catch(function(){});
}
window.XA={
  post:function(){
    if(needLogin())return;
    formModal('发布活动',[
      {k:'category',label:'类别',type:'select',options:['社团招新','讲座信息','比赛组队','拼车/拼单','自习搭子','运动搭子','饭搭子','实验招募','问卷互填']},
      {k:'title',label:'标题',full:1},
      {k:'body',label:'详情',type:'textarea',full:1}
    ],function(v,close){
      api('/api/activity','POST',v).then(function(){close();toast('已发布');loadActivity()}).catch(function(e){toast(e.message)});
    });
  },
  join:function(id){
    if(needLogin())return;
    api('/api/activity/'+id+'/join','POST').then(function(r){toast('已加入！发起人会收到通知');loadActivity()})
      .catch(function(e){toast(e.message)});
  }
};

/* ================= 公告中心 ================= */
function loadAnnouncements(){
  api('/api/announcements').then(function(list){
    var d=dynArea('announce','.card');if(!d)return;
    var h='';
    list.forEach(function(a){
      h+='<div class="card"'+(a.level==='强提醒'?' style="border-left:4px solid var(--stamp)"':'')+'>'
        +'<h3>'+esc(a.title)+' <span class="tag '+(a.level==='强提醒'?'red':'gray')+'">'+esc(a.level)+'</span></h3>'
        +'<p style="font-size:13px">'+esc(a.body)+'</p>'
        +'<p class="muted" style="margin-top:6px">已读确认：<b class="mono">'+a.read_count+'</b> 人 · '
        +(a.read?'<span class="tag green">✓ 你已阅读</span>'
          :'<button class="btn ghost sm" onclick="XN.read('+a.id+')">我已阅读</button>')+'</p></div>';
    });
    d.innerHTML=h;
  }).catch(function(){});
}
window.XN={read:function(id){
  if(needLogin())return;
  api('/api/announcements/'+id+'/read','POST').then(function(){toast('已确认阅读 ✓');loadAnnouncements()})
    .catch(function(e){toast(e.message)});
}};

/* ================= 我的信用页：头衔 + 收件箱 ================= */
function loadCreditExtras(){
  if(!TOKEN)return;
  var pg=document.getElementById('page-credit');
  var d=pg.querySelector('.dyn-inbox');
  if(!d){d=document.createElement('div');d.className='dyn-inbox';pg.appendChild(d)}
  Promise.all([api('/api/my-titles'),api('/api/notifications'),api('/api/dm')]).then(function(rs){
    var t=rs[0],ns=rs[1],dms=rs[2];
    var h='<div class="card"><h3>🏅 我的头衔与经验</h3><p style="font-size:13px">经验 <b class="mono">'+t.exp+'</b> · 被采纳 '+t.accepted+' 次 · 加精 '+t.featured+' 篇</p>'
      +'<p style="margin-top:4px">'+(t.titles.length?t.titles.map(function(x){return '<span class="tag yellow">'+esc(x)+'</span>'}).join(''):'<span class="muted">暂无头衔，答题/投稿赢取</span>')+'</p></div>';
    h+='<div class="card"><h3>🔔 站内通知</h3>';
    ns.slice(0,6).forEach(function(n){h+='<li style="list-style:none;font-size:12.5px;margin-bottom:5px">'+(n.is_read?'':'<span class="dot" style="color:var(--stamp)">● </span>')+esc(n.content)+'</li>'});
    if(!ns.length)h+='<p class="muted">暂无通知</p>';
    h+='</div><div class="card"><h3>✉️ 私信（新用户每日 5 条上限）</h3>';
    dms.slice(0,5).forEach(function(m){h+='<li style="list-style:none;font-size:12.5px;margin-bottom:5px"><b>'+esc(m.from)+'</b> → '+esc(m.to)+'：'+esc(m.body)+'</li>'});
    h+='<button class="btn ghost sm" onclick="XD.send()">写私信</button></div>';
    d.innerHTML=h;
  }).catch(function(){});
}
window.XD={send:function(){
  if(needLogin())return;
  formModal('发私信（交易/组队/问答请优先用快捷模板）',[
    {k:'to_nickname',label:'收件人昵称',ph:'如：车头_老K'},
    {k:'body',label:'内容',type:'textarea',full:1}
  ],function(v,close){
    api('/api/dm','POST',v).then(function(){close();toast('已发送')}).catch(function(e){toast(e.message)});
  });
}};

/* ================= 路由 hook ================= */
var LOADERS=window.XLOADERS={qa:loadQA,handbook:loadHandbook,course:loadCourses,observe:loadObserve,
             gov:loadGov,lost:loadLost,activity:loadActivity,announce:loadAnnouncements,
             credit:loadCreditExtras};
var _origShowPage=window.showPage;
window.showPage=function(id){
  _origShowPage(id);
  if(X_LIVE&&LOADERS[id])LOADERS[id]();
};

/* ================= 启动 ================= */
api('/api/qa').then(function(){
  X_LIVE=true;
  console.log('[梧桐墙] 扩展板块已连接后端');
}).catch(function(){});
})();
