import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
const source = await readFile(new URL('../../utils/sendAmount.ts', import.meta.url), 'utf8')
const {outputText} = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}})
const {validateSendAmount:v,validateSendDispatch:dispatch}=await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`)
for(const [text,precision,balance,expected] of [
 ['',0,'1000','required'],['0',0,'1000','positive'],['-1',0,'1000','decimal'],['1e+24',0,'1000','decimal'],['1E3',0,'1000','decimal'],[' 1',0,'1000','decimal'],
 ['1.5',0,'1000','precision'],['1001',0,'1000','balance'],['1000000000000000000000000',0,'1000','balance'],['1000',0,'1000',null],['330',0,'1000',null],
 ['1.23',2,'1.24',null],['1.234',2,'9','precision'],['9007199254740993.01',2,'9007199254740993.02',null],['9007199254740993.03',2,'9007199254740993.02','balance'],
 ['1',undefined,'9','unavailable'],['1',0,1e24,'unavailable'],['1',0,null,'unavailable']
]) assert.equal(v(text,precision,balance),expected,`${text}/${precision}/${balance}`)
assert.equal(dispatch('900',0,'1000','scope-a','scope-a'),null)
assert.equal(dispatch('900',0,'800','scope-a','scope-a'),'balance')
assert.equal(dispatch('900',0,'1000','scope-a','scope-b'),'scope')
console.log('Send amount decimal/precision/balance/dispatch checks passed')
