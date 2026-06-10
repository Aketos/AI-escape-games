# 🎮 Document de Contexte Fonctionnel : Escape Game Audio "CHRONOS"

## 1. Concept Général
Le projet est un **Escape Game audio** où l'interface principale est la voix. Le joueur interagit avec une intelligence artificielle nommée **CHRONOS**, qui fait office de Maître du Jeu et de PNJ (Personnage Non-Joueur) principal. Le jeu se distingue par sa volonté de maintenir un cadre narratif strict, évitant que le joueur ne sorte de l'histoire prévue [cite:325][cite:326].

## 2. Déroulement du Jeu et Introduction

### A. L'Introduction (Onboarding)
L'introduction a pour but d'immerger le joueur tout en lui expliquant subtilement les règles d'interaction :
- **Prise de contact :** CHRONOS initie le dialogue, posant le cadre spatial et temporel de l'intrigue.
- **Calibrage vocal :** L'introduction sert de test technique déguisé. CHRONOS demande au joueur de confirmer son identité ou son statut (ex: "M'entendez-vous, Agent ?"), s'assurant que le micro fonctionne et que le joueur comprend comment répondre.
- **Établissement du cadre narratif :** CHRONOS expose l'objectif principal de la salle (la mission à accomplir ou le mystère à résoudre) et pose les limites de ce qu'il peut faire ou comprendre, justifiant narrativement ses futures restrictions.

### B. Boucle de Gameplay (Gameplay Loop)
La progression se fait par itérations vocales :
1. **Observation/Analyse :** Le joueur décrit ce qu'il voit ou pose des questions sur son environnement ("Que vois-je sur le bureau ?").
2. **Traitement par CHRONOS :** L'IA analyse la requête en la confrontant à ses directives (System Prompt) et à l'état actuel de la salle.
3. **Résolution/Blocage :** 
   - Si la déduction du joueur est correcte ou l'action pertinente, CHRONOS valide l'action et donne un nouvel indice ou débloque une étape.
   - Si le joueur s'égare, CHRONOS le recadre fermement pour le maintenir dans la trame narrative stricte [cite:325].
4. **Progression :** Les énigmes sont résolues séquentiellement, menant à la conclusion de l'Escape Game.

## 3. Mécaniques de Contrôle Narratif

Pour éviter les "hallucinations" de l'IA et garantir une expérience d'Escape Game cohérente, des mécaniques spécifiques de contrôle narratif sont implémentées :

- **Le "Railroading" justifié :** CHRONOS n'est pas un assistant ouvert (comme Alexa ou Siri) [cite:326]. Il a une personnalité dirigiste et ramènera toujours le joueur à sa tâche. Si le joueur demande la recette des crêpes, CHRONOS simulera une interférence, une incompréhension, ou le réprimandera pour son manque de concentration.
- **Gestion des État (State Management) :** Le backend (Go) gère l'état d'avancement du joueur. Le comportement et les connaissances de CHRONOS évoluent en fonction des étapes franchies, l'empêchant de révéler des indices prématurément.
- **Le System Prompt dynamique :** Le prompt qui dicte le comportement de CHRONOS est régulièrement mis à jour par le moteur de jeu (C#) pour refléter l'évolution de la situation dans la salle [cite:324].