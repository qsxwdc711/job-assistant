import { test, expect, type Page } from '@playwright/test'
import { join } from 'node:path'
import type { Resume, ResumeDetail, Recommendation } from '../../src/shared/api/types'

test.skip(process.env.S3_BROWSER_FIXTURE !== 'true', 'requires the dedicated S3 fixture and isolated model')
const password=process.env.S2_BROWSER_PASSWORD
async function login(page:Page,name:string){if(!password)throw new Error('isolated fixture password missing');await page.goto('/login');await page.getByLabel('账号',{exact:true}).fill(name);await page.getByLabel('密码',{exact:true}).fill(password);await page.getByRole('button',{name:'登录',exact:true}).click();await expect(page).toHaveURL(/\/jobs$/)}

test('VIP can review a synthetic resume, confirm a version and compare a job on desktop and phone',async({browser,baseURL})=>{
 const desktopContext=await browser.newContext({baseURL,viewport:{width:1440,height:1000}}),mobileContext=await browser.newContext({baseURL,viewport:{width:390,height:844},isMobile:true,hasTouch:true}),otherContext=await browser.newContext({baseURL})
 const desktop=await desktopContext.newPage(),mobile=await mobileContext.newPage(),other=await otherContext.newPage(),errors:string[]=[]
 for(const page of [desktop,mobile,other])page.on('pageerror',e=>errors.push(e.message))
 try{
  // S2 deliberately hides the fixture job; publish it again using the current administrator's CSRF token.
  const auth=await (await desktopContext.request.post('/api/v1/auth/login',{data:{username:'web-admin',password}})).json()
  const jobs=await (await desktopContext.request.get('/api/v1/admin/jobs')).json()
  expect(jobs.items).toHaveLength(1)
  expect((await desktopContext.request.patch('/api/v1/admin/jobs/'+jobs.items[0].id,{headers:{'X-CSRF-Token':auth.csrf_token},data:{published:true,note:'S3 isolated fixture republish'}})).status()).toBe(204)
  await desktopContext.clearCookies()
  await login(desktop,'web-vip-a');await desktop.getByRole('navigation',{name:'主导航'}).getByRole('link',{name:'简历资料'}).click()
  await desktop.getByLabel('需要隐去的姓名或别名').fill('张三')
  await desktop.getByLabel('上传 PDF / DOCX').setInputFiles(join(import.meta.dirname,'fixtures','synthetic.docx'))
  await desktop.getByRole('button',{name:'上传并本地提取',exact:true}).click()
  const preview=desktop.getByLabel('将用于模型处理的文本')
  await expect(preview).toBeVisible({timeout:20000});await expect(preview).toHaveValue(/使用 Go 开发接口/)
  const text=await preview.inputValue();for(const pii of ['张三','13800138000','synthetic@example.com','北京市海淀区测试路1号'])expect(text).not.toContain(pii)
  await expect(desktop.getByRole('button',{name:'使用模型生成待确认草稿'})).toBeDisabled()
  await preview.fill(text+'\n已移除合成项目名称')
  await desktop.getByRole('button',{name:'保存脱敏预览',exact:true}).click()
  await expect(desktop.getByRole('button',{name:'保存脱敏预览',exact:true})).toBeDisabled()
  await desktop.getByRole('checkbox',{name:/我已核对预览/}).check()
  await desktop.getByRole('button',{name:'使用模型生成待确认草稿'}).click()
  await expect(desktop.getByText('查看草稿原文依据',{exact:true})).toBeVisible({timeout:20000})
  await desktop.getByLabel('学历地区',{exact:true}).selectOption('domestic')
  await desktop.getByLabel('毕业日期',{exact:true}).fill('2027-06-30')
  await desktop.getByRole('checkbox',{name:'计算机科学',exact:true}).check()
  await desktop.getByRole('checkbox',{name:/我已核对预览/}).check()
  await desktop.getByRole('button',{name:'核对完成，保存为当前资料',exact:true}).click()
  await expect(desktop.getByText('当前版本',{exact:true})).toBeVisible()
  await desktop.screenshot({path:test.info().outputPath('desktop-resume.png'),fullPage:true})
  const resumeList=await (await desktopContext.request.get('/api/v1/me/resumes')).json() as {items:Resume[]}
  expect(resumeList.items).toHaveLength(1)
  const resume=resumeList.items[0]!
  await desktop.getByRole('navigation',{name:'主导航'}).getByRole('link',{name:'岗位匹配'}).click()
  await desktop.getByRole('checkbox',{name:/后台开发/}).check()
  await desktop.getByRole('button',{name:/评估所选岗位/}).click()
  await expect(desktop.getByRole('heading',{name:'语义相关性：中',exact:true})).toBeVisible({timeout:20000})
  await expect(desktop.getByText('仍有条件待确认',{exact:true}).first()).toBeVisible()
  await expect(desktop.getByText('合成资料包含 Go 接口实践',{exact:true})).toBeVisible()
  await expect(desktop.getByText('资料中未找到网络协议实践证据，需进一步确认',{exact:true})).toBeVisible()
  await desktop.screenshot({path:test.info().outputPath('desktop-matching.png'),fullPage:true})
  const recs=await (await desktopContext.request.get('/api/v1/me/recommendations')).json() as {items:Recommendation[]};expect(recs.items).toHaveLength(1)
  await login(mobile,'web-vip-a');await mobile.getByRole('navigation',{name:'手机导航'}).getByRole('link',{name:'岗位匹配'}).click()
  await expect(mobile.getByRole('heading',{name:'语义相关性：中',exact:true})).toBeVisible()
  expect(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await mobile.screenshot({path:test.info().outputPath('mobile-matching.png'),fullPage:true})
  await mobile.getByRole('navigation',{name:'手机导航'}).getByRole('link',{name:'简历资料'}).click()
  await mobile.getByRole('button',{name:/synthetic.docx/}).click();await expect(mobile.getByText('当前版本',{exact:true})).toBeVisible()
  expect(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await mobile.screenshot({path:test.info().outputPath('mobile-resume.png'),fullPage:true})
  const before=await (await desktopContext.request.get('/api/v1/me/resumes/'+resume.id)).json() as ResumeDetail
  expect(before.versions).toHaveLength(1)
  await login(other,'web-vip-b')
  for(const suffix of ['', '/download'])expect((await otherContext.request.get('/api/v1/me/resumes/'+resume.id+suffix)).status()).toBe(404)
  expect((await otherContext.request.get('/api/v1/me/recommendations/'+recs.items[0]!.id)).status()).toBe(404)
  await other.goto('/resumes');await expect(other.getByText('还没有上传简历。',{exact:true})).toBeVisible()
  mobile.once('dialog',dialog=>dialog.accept())
  await mobile.getByRole('button',{name:'删除',exact:true}).click();await expect(mobile.getByText('还没有上传简历。',{exact:true})).toBeVisible()
  expect((await desktopContext.request.get('/api/v1/me/resumes/'+resume.id)).status()).toBe(404)
  expect((await desktopContext.request.get('/api/v1/me/recommendations/'+recs.items[0]!.id)).status()).toBe(404)
  expect(await desktop.evaluate(()=>localStorage.length)).toBe(0);expect(errors).toEqual([])
 }finally{await desktopContext.close();await mobileContext.close();await otherContext.close()}
})

test('ordinary account sees a clear capability boundary',async({page})=>{
 await login(page,'web-user-b');await page.goto('/resumes');await expect(page.getByText('上传、阅读和匹配需要 VIP 权限。',{exact:true})).toBeVisible();await expect(page.locator('input[type=file]')).toHaveCount(0)
 await page.goto('/recommendations');await expect(page.getByRole('heading',{name:'岗位匹配需要 VIP 权限'})).toBeVisible()
})
