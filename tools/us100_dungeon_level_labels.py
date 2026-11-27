"""Localize the dungeon level-gap warning and its close variants."""

BASE = "3f1f43b42c0809592e09f3ac040703eb0900d0f24e245d80d4d998cdd52dfc5f"
PVF = "6191877bca4012fe912afc92243ab98f606c82b49aeca7340e509b6123445b05"
QUEST = "ef224c2064c8c156119f92c303a887a9db8922b3d766f7f857e9f55bdcc77c0b"
ROWS = {
    "100007193": ("EXP and resource penalty due to significant Level gap between dungeon and your character.",
                  "角色等级与地下城等级相差过大，获得的经验和资源将减少。"),
    "100002291": ("You can't gain EXP outside Optimal Level dungeons.",
                  "在推荐等级范围以外的地下城中无法获得经验。"),
    "100088092": ("This dungeon's dimension is lower than your Optimal Level. Missions can't be completed in it.",
                  "该地下城的次元等级低于你的推荐等级，无法在此完成任务。"),
    "100088447": ("Can't enter this dungeon because of the Level gap among your party members.",
                  "队员之间的等级差距过大，无法进入该地下城。"),
    "44513": ("Exceeds the Season server's maximum Level gap allowed among party members (%d). The party cannot be formed.",
              "队员等级差距超过赛季服务器上限（%d级），无法组队。"),
}
