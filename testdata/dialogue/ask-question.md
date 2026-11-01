user: 用 AskUserQuestion 問我兩題（一題單選、一題複選），收到答案後複述。

agent: 好，我來問兩題。

<tool AskUserQuestion | 先做哪個方案？>
<answer | 乙案；紅、藍, 深>

agent: 你選了乙案，顏色是紅與藍, 深。

user: 再問一題，我會自己打字回答。

<tool AskUserQuestion | 幾點開會？>
<answer | 晚上七點>

agent: 你回答晚上七點。

user: 再問一題，我不想回答。

<tool AskUserQuestion | 吃什麼？>

agent: 好，不問了。

user: 最後一題，等我回答。

<tool AskUserQuestion | 去哪裡？>
